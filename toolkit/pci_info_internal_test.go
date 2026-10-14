// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func TestSysfsPCIInfo(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "0001:65:00.0")
	writePCIFixture(t, path, "0x030200\n")
	driver := filepath.Join(t.TempDir(), "example-driver")
	if err := os.Mkdir(driver, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driver, filepath.Join(path, "driver")); err != nil {
		t.Fatal(err)
	}
	backend := &sysfsPCIeBackend{root: root}
	info, err := backend.PCIInfo(context.Background())
	if err != nil || len(info) != 1 {
		t.Fatalf("PCIInfo: %+v, error=%v", info, err)
	}
	m := info[0].Identity
	if len(m.Errors()) != 0 || len(info[0].Link.Errors()) != 0 {
		t.Fatalf("collection errors: %v, %v", m.Errors(), info[0].Link.Errors())
	}
	d := m.Device
	if d.Link == nil || d.Link.CurrentGeneration == nil || *d.Link.CurrentGeneration != 5 || d.Link.MaxGeneration == nil || *d.Link.MaxGeneration != 6 || *d.Link.CurrentSpeedGTPerSec != 32 || *d.Link.MaxSpeedGTPerSec != 64 || *d.Link.CurrentWidth != 16 || *d.Link.MaxWidth != 16 {
		t.Fatalf("PCIe capabilities: %+v", d.Link)
	}
	if d.Locality == nil || *d.Locality.NUMANode != 0 || *d.Locality.LocalCPUs != "0-3,8" || d.Driver == nil || *d.Driver != "example-driver" {
		t.Fatalf("locality/driver: %+v", d)
	}
	if d.SubsystemVendorID == nil || *d.SubsystemVendorID != 0x10de || d.SubsystemDeviceID == nil || *d.SubsystemDeviceID != 0x4321 || d.Revision == nil || *d.Revision != 0xa1 {
		t.Fatalf("board identity: %+v", d)
	}
	if d.SRIOV == nil || *d.SRIOV.TotalVFs != 0 || *d.SRIOV.EnabledVFs != 0 || *d.SRIOV.PhysicalFunctionBDF != "" || len(d.SRIOV.VirtualFunctionBDFs) != 0 {
		t.Fatalf("non-SR-IOV device: %+v", d.SRIOV)
	}
	if err = catalog.CheckDerivedSignals(m); err != nil {
		t.Fatal(err)
	}
	if err = catalog.CheckDerivedSignalKeysStable(m); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%T PCI record:\n%s", backend, data)
	var roundTrip pcie.PCIDevice
	if err = json.Unmarshal(data, &roundTrip); err != nil || roundTrip.Link == nil || *roundTrip.Link.MaxGeneration != 6 || roundTrip.Locality == nil || *roundTrip.Locality.NUMANode != 0 {
		t.Fatalf("PCI JSON: %s, error=%v", data, err)
	}
	measurements, err := backend.MeasurePCIe(context.Background())
	if err != nil || len(measurements) != 4 || measurements[1].(pcie.LinkMeasurement).SpeedGTPerSec != 32 {
		t.Fatalf("sysfs measurements: %v, error=%v", measurements, err)
	}
}

func TestPCIInfoSRIOV(t *testing.T) {
	root := t.TempDir()
	pf := filepath.Join(root, "0000:65:00.0")
	vf := filepath.Join(root, "0000:65:00.1")
	writePCIFixture(t, pf, "0x020000\n")
	writePCIFixture(t, vf, "0x020000\n")
	if err := os.Symlink(vf, filepath.Join(pf, "virtfn0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pf, filepath.Join(vf, "physfn")); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"sriov_totalvfs": "8\n", "sriov_numvfs": "1\n"} {
		if err := os.WriteFile(filepath.Join(pf, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	info, err := (&sysfsPCIeBackend{root: root}).PCIInfo(context.Background())
	if err != nil || len(info) != 2 {
		t.Fatalf("SR-IOV discovery: %v, error=%v", info, err)
	}
	physical := info[0].Identity.Device.SRIOV
	virtual := info[1].Identity.Device.SRIOV
	if *physical.TotalVFs != 8 || *physical.EnabledVFs != 1 || !slices.Equal(physical.VirtualFunctionBDFs, []string{"0000:65:00.1"}) || *virtual.PhysicalFunctionBDF != "0000:65:00.0" {
		t.Fatalf("SR-IOV relationships: PF=%+v, VF=%+v", physical, virtual)
	}
}

func TestPCIInfoUnknownProperties(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "0000:65:00.0")
	writePCIFixture(t, path, "0x030200\n")
	for name, value := range map[string]string{
		"max_link_speed": "256 GT/s PCIe\n", "max_link_width": "invalid\n",
		"numa_node": "-1\n", "local_cpulist": "bad-list\n", "revision": "0x100\n",
	} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "missing-driver"), filepath.Join(path, "driver")); err != nil {
		t.Fatal(err)
	}
	info, err := (&sysfsPCIeBackend{root: root}).PCIInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := info[0].Identity
	d := m.Device
	if *d.Link.MaxSpeedGTPerSec != 256 || d.Link.MaxGeneration != nil || d.Link.MaxWidth != nil || *d.Locality.NUMANode != -1 || d.Locality.LocalCPUs != nil || d.Driver != nil || d.Revision != nil || d.Link.CurrentGeneration == nil || *d.Link.CurrentGeneration != 5 {
		t.Fatalf("partial properties: %+v", d)
	}
	for _, signal := range []string{pcie.SignalPCIMaxGeneration, pcie.SignalPCIMaxLinkWidth, pcie.SignalPCILocalCPUs, pcie.SignalPCIDriver, pcie.SignalPCIRevision} {
		if !pciSignalFailed(m.Errors(), signal) {
			t.Errorf("missing error for %s: %v", signal, m.Errors())
		}
	}
	if pciSignalFailed(m.Errors(), pcie.SignalPCIMaxLinkSpeed) || pciSignalFailed(m.Errors(), pcie.SignalPCINUMANode) || pciSignalFailed(m.Errors(), pcie.SignalVendorID) {
		t.Fatalf("usable signals marked failed: %v", m.Errors())
	}
}

func TestPCIGenerationsAndCancellation(t *testing.T) {
	for index, speed := range []float64{2.5, 5, 8, 16, 32, 64, 128} {
		if generation := pciGeneration(speed); generation == nil || *generation != int64(index+1) {
			t.Errorf("generation for %g GT/s: %v", speed, generation)
		}
	}
	if pciGeneration(0) != nil || pciGeneration(100) != nil {
		t.Fatal("unknown speed assigned a generation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SysfsPCIInfo(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery cancellation: %v", err)
	}
	if _, err := SysfsPCIe().MeasurePCIe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("backend cancellation: %v", err)
	}
}

func TestPCIInfoMissingPropertiesPreserveIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "0000:65:00.0")
	writePCIFixture(t, path, "0x030200\n")
	for _, name := range []string{"max_link_speed", "max_link_width", "numa_node", "local_cpulist", "subsystem_vendor", "subsystem_device", "revision"} {
		if err := os.Remove(filepath.Join(path, name)); err != nil {
			t.Fatal(err)
		}
	}
	info, err := (&sysfsPCIeBackend{root: root}).PCIInfo(context.Background())
	if err != nil || len(info) != 1 {
		t.Fatalf("partial discovery: %v, error=%v", info, err)
	}
	m := info[0].Identity
	d := m.Device
	if d.BDF != "0000:65:00.0" || d.VendorID != 0x10de || d.DeviceID != 0x1234 || d.Type != "gpu" {
		t.Fatalf("lost device identity: %+v", d)
	}
	if d.Link.MaxSpeedGTPerSec != nil || d.Link.MaxGeneration != nil || d.Link.MaxWidth != nil || d.Locality.NUMANode != nil || d.Locality.LocalCPUs != nil || d.SubsystemVendorID != nil || d.SubsystemDeviceID != nil || d.Revision != nil {
		t.Fatalf("missing properties reported as known: %+v", d)
	}
	if d.Driver == nil || *d.Driver != "" || d.SRIOV.TotalVFs == nil || *d.SRIOV.TotalVFs != 0 {
		t.Fatalf("known absent optional properties reported as unreadable: %+v", d)
	}
	want := []string{pcie.SignalPCIMaxLinkSpeed, pcie.SignalPCIMaxGeneration, pcie.SignalPCIMaxLinkWidth, pcie.SignalPCINUMANode, pcie.SignalPCILocalCPUs, pcie.SignalPCISubsystemVendorID, pcie.SignalPCISubsystemDeviceID, pcie.SignalPCIRevision}
	var failed []string
	for _, failure := range m.Errors() {
		if !errors.Is(failure.Err(), os.ErrNotExist) {
			t.Errorf("lost missing-file cause: %v", failure.Err())
		}
		failed = append(failed, failure.FailedSignals()...)
	}
	slices.Sort(failed)
	slices.Sort(want)
	if !slices.Equal(failed, want) || len(info[0].Link.Errors()) != 0 {
		t.Fatalf("incorrect failure attribution: %v, link=%v", failed, info[0].Link.Errors())
	}
}

func TestPCIInfoSRIOVPartialRelationships(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "0000:65:00.0")
	writePCIFixture(t, path, "0x020000\n")
	vf := filepath.Join(root, "0000:65:00.1")
	if err := os.Mkdir(vf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(vf, filepath.Join(path, "virtfn0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing-vf"), filepath.Join(path, "virtfn1")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "sriov_totalvfs"), []byte("8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "sriov_numvfs"), []byte("-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := collectPCIDeviceInfo(context.Background(), path, pcie.PCIeMeasurement{Device: pcie.PCIDevice{BDF: "0000:65:00.0"}}).Identity
	info := m.Device.SRIOV
	if info.TotalVFs == nil || *info.TotalVFs != 8 || info.EnabledVFs != nil || !slices.Equal(info.VirtualFunctionBDFs, []string{"0000:65:00.1"}) {
		t.Fatalf("lost usable SR-IOV metadata: %+v", info)
	}
	if !pciSignalFailed(m.Errors(), pcie.SignalPCIEnabledVFs) || !pciSignalFailed(m.Errors(), pcie.SignalPCIVirtualFunctions) || pciSignalFailed(m.Errors(), pcie.SignalPCITotalVFs) {
		t.Fatalf("incorrect SR-IOV failure attribution: %v", m.Errors())
	}
}

func TestSysfsPCIInfoPreservesHealthVerdicts(t *testing.T) {
	for _, scenario := range []string{"metadata_failure", "degraded_link", "unavailable_link"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			const bdf = "0000:65:00.0"
			path := filepath.Join(root, bdf)
			writePCIFixture(t, path, "0x030200\n")
			want := health.Pass
			switch scenario {
			case "metadata_failure":
				if err := os.Symlink(filepath.Join(root, "missing-driver"), filepath.Join(path, "driver")); err != nil {
					t.Fatal(err)
				}
			case "degraded_link":
				if err := os.WriteFile(filepath.Join(path, "current_link_width"), []byte("8\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				want = health.Critical
			case "unavailable_link":
				if err := os.Remove(filepath.Join(path, "current_link_width")); err != nil {
					t.Fatal(err)
				}
				want = health.Unknown
			}
			cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra), catalog.WithOverrides(catalog.NewOverrideSet(
				catalog.Override{CheckID: "pcie_link_width", ThresholdName: "expected_link_width_by_bdf", Value: map[string]int64{bdf: 16}},
			)))
			cat.MustRegister(pcie.LinkWidth())
			backend := &sysfsPCIeBackend{root: root}
			verdict, err := Collect(cat, NewPCIeSensor(backend))
			if err != nil || len(verdict.CheckResults) != 1 || verdict.CheckResults[0].Severity != want || verdict.OverallSeverity != want {
				t.Fatalf("sysfs verdict: %+v, error=%v; want %v", verdict, err, want)
			}
		})
	}
}

// Restricted configuration space must preserve accessible device metadata.
func TestPCIInfoRestrictedCapabilities(t *testing.T) {
	root := t.TempDir()
	const bdf = "0001:65:00.0"
	path := filepath.Join(root, bdf)
	writePCIFixture(t, path, "0x030200\n")
	if err := os.WriteFile(filepath.Join(path, "config"), make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"current_link_speed", "current_link_width", "max_link_speed", "max_link_width"} {
		if err := os.Remove(filepath.Join(path, name)); err != nil {
			t.Fatal(err)
		}
	}
	driver := filepath.Join(t.TempDir(), "nvidia")
	if err := os.Mkdir(driver, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driver, filepath.Join(path, "driver")); err != nil {
		t.Fatal(err)
	}
	group := filepath.Join(t.TempDir(), "42")
	if err := os.MkdirAll(filepath.Join(group, "devices"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(group, "devices", bdf)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(group, filepath.Join(path, "iommu_group")); err != nil {
		t.Fatal(err)
	}
	backend := &sysfsPCIeBackend{root: root}
	devices, err := backend.PCIInfo(context.Background())
	if err != nil || len(devices) != 1 {
		t.Fatalf("restricted discovery: %v, error=%v", devices, err)
	}
	m := devices[0].Identity
	d := m.Device
	if d.BDF != bdf || d.Type != "gpu" || d.VendorID != 0x10de || d.DeviceID != 0x1234 || d.Revision == nil || *d.Revision != 0xa1 {
		t.Fatalf("lost identity: %+v", d)
	}
	if d.IOMMU == nil || d.IOMMU.GroupID != 42 || !slices.Equal(d.IOMMU.Devices, []string{bdf}) || d.Driver == nil || *d.Driver != "nvidia" || d.Locality.NUMANode == nil || *d.Locality.NUMANode != 0 {
		t.Fatalf("lost accessible metadata: %+v", d)
	}
	if d.ACS != nil || d.Link.CurrentGeneration != nil || d.Link.MaxGeneration != nil || d.Link.CurrentSpeedGTPerSec != nil || d.Link.CurrentWidth != nil || d.Link.MaxSpeedGTPerSec != nil || d.Link.MaxWidth != nil {
		t.Fatalf("inaccessible capabilities reported as known: %+v", d)
	}
	for _, signal := range []string{pcie.SignalACSSupported, pcie.SignalACSCapabilities, pcie.SignalACSControl, pcie.SignalPCICurrentGeneration, pcie.SignalPCIMaxGeneration, pcie.SignalPCIMaxLinkSpeed, pcie.SignalPCIMaxLinkWidth} {
		if !pciSignalFailed(m.Errors(), signal) {
			t.Errorf("missing error for %s", signal)
		}
	}
	if !pciSignalFailed(devices[0].Link.Errors(), pcie.SignalLinkSpeed) || !pciSignalFailed(devices[0].Link.Errors(), pcie.SignalLinkWidth) {
		t.Fatalf("inaccessible negotiated link signals: %v", devices[0].Link.Errors())
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["acs"] != nil || payload["link"].(map[string]any)["current_generation"] != nil {
		t.Fatalf("JSON must preserve unknown capabilities: %s", data)
	}
}

func TestPCIInventoryFromMeasurements(t *testing.T) {
	const bdf = "0000:65:00.0"
	const peer = "0000:66:00.0"
	revision := uint8(0xa1)
	subsystem := uint16(0x10de)
	m := pcie.PCIeMeasurement{Device: pcie.PCIDevice{
		BDF: bdf, ParentBDF: "0000:64:00.0", Type: "gpu", VendorID: 0x10de, DeviceID: 0x1234,
		ClassCode: 0x030200, Revision: &revision, SubsystemVendorID: &subsystem,
		IOMMU: &pcie.IOMMUInfo{GroupID: 7, Devices: []string{bdf}},
		Link:  &pcie.PCILinkInfo{},
	}}
	m.AddError(errors.New("revision unavailable"), pcie.SignalPCIRevision)
	link := pcie.LinkMeasurement{BDF: bdf, Width: 0, SpeedGTPerSec: 32}
	link.AddError(errors.New("width unavailable"), pcie.SignalLinkWidth)
	counters := pcie.ErrorMeasurement{BDF: bdf, CorrectableErrors: 9223372036854775807, FatalErrors: 9}
	counters.AddError(errors.New("fatal count unavailable"), pcie.SignalFatalErrors)
	// Link and counters preceding identity must still enrich the same record.
	inventory, err := PCIInventoryFromMeasurements(counters, link, m, pcie.InventoryMeasurement{BDFs: []string{peer, bdf}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Devices) != 2 || len(inventory.FieldErrors) != 0 {
		t.Fatalf("inventory: %+v", inventory)
	}
	device := inventory.Devices[0]
	if device["bdf"] != bdf || device["vendor_id"] != "0x10de" || device["device_id"] != "0x1234" || device["class_code"] != "0x030200" || device["subsystem_vendor_id"] != "0x10de" || device["revision"] != nil {
		t.Fatalf("device identity: %+v", device)
	}
	if link := device["link"].(map[string]any); link["current_width"] != nil || link["current_speed_gt_per_sec"] != float64(32) {
		t.Fatalf("device link: %+v", link)
	}
	if aer := device["aer"].(map[string]any); aer["fatal_errors"] != nil || aer["correctable_errors"] != int64(9223372036854775807) {
		t.Fatalf("AER values: %+v", aer)
	}
	if errors := device["field_errors"].(map[string][]string); len(errors) != 3 || len(errors["link.current_width"]) != 1 {
		t.Fatalf("field errors: %+v", errors)
	}
	if inventory.Devices[1]["bdf"] != peer || inventory.Devices[1]["aer"] != nil || inventory.Devices[1]["vendor_id"] != nil {
		t.Fatalf("inventory-only device fabricated values: %+v", inventory.Devices[1])
	}
	fromPointers, err := PCIInventoryFromMeasurements(&counters, &link, &m, &pcie.InventoryMeasurement{BDFs: []string{peer, bdf}})
	if err != nil {
		t.Fatal(err)
	}
	pointerJSON, err := json.Marshal(fromPointers)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(inventory)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("inventory serialization: %s; err=%v", encoded, err)
	}
	if string(pointerJSON) != string(encoded) {
		t.Fatalf("pointer inventory differs: %s; want %s", pointerJSON, encoded)
	}
}

func TestPCIInventoryEmptyAndDiscoveryFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		var measurement health.Measurement = pcie.InventoryMeasurement{BDFs: []string{}}
		if failed {
			measurement = pcie.ErroredInventory(errors.New("discovery failed"))
		}
		inventory, err := PCIInventoryFromMeasurements(measurement)
		if err != nil {
			t.Fatal(err)
		}
		if inventory.Devices == nil || len(inventory.Devices) != 0 || (len(inventory.FieldErrors) > 0) != failed {
			t.Fatalf("empty/discovery inventory: %+v", inventory)
		}
	}
}
