// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func TestSysfsPCIIsolation(t *testing.T) {
	root := t.TempDir()
	bus := filepath.Join(root, "pci")
	path := filepath.Join(bus, "0001:65:00.0")
	writePCIFixture(t, path, "0x030200\n")
	groupPath := filepath.Join(root, "iommu_groups", "42")
	if err := os.MkdirAll(filepath.Join(groupPath, "devices"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bdf := range []string{"0001:65:00.0", "0001:65:00.1"} {
		if err := os.Symlink(path, filepath.Join(groupPath, "devices", bdf)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(groupPath, filepath.Join(path, "iommu_group")); err != nil {
		t.Fatal(err)
	}
	config := make([]byte, 4096)
	binary.LittleEndian.PutUint32(config[0x100:], 0x14000001|1<<16)
	binary.LittleEndian.PutUint32(config[0x140:], 0x000d|1<<16)
	binary.LittleEndian.PutUint16(config[0x144:], 0x017f)
	binary.LittleEndian.PutUint16(config[0x146:], 0x001d)
	if err := os.WriteFile(filepath.Join(path, "config"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &sysfsPCIeBackend{root: bus}
	measurements, err := backend.MeasurePCIe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := measurements[0].(pcie.PCIeMeasurement)
	if len(m.Errors()) != 0 {
		t.Fatalf("isolation collection errors: %v", m.Errors())
	}
	if group := m.Device.IOMMU; group == nil || group.GroupID != 42 || !slices.Equal(group.Devices, []string{"0001:65:00.0", "0001:65:00.1"}) {
		t.Fatalf("IOMMU group: %+v", group)
	}
	if acs := m.Device.ACS; acs == nil || !acs.Supported || acs.Capabilities != 0x017f || acs.Control != 0x001d {
		t.Fatalf("ACS: %+v", acs)
	}
}

func TestPCIIsolationAbsentAndUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "0000:65:00.0")
	writePCIFixture(t, path, "0x030200\n")
	m := pcie.PCIeMeasurement{Device: pcie.PCIDevice{BDF: "0000:65:00.0"}}
	collectPCIIsolation(context.Background(), path, &m)
	if len(m.Errors()) != 0 || m.Device.ACS == nil || m.Device.ACS.Supported || m.Device.IOMMU == nil || m.Device.IOMMU.GroupID != -1 {
		t.Fatalf("absent isolation metadata: %+v, errors=%v", m.Device, m.Errors())
	}
	if err := os.WriteFile(filepath.Join(path, "config"), make([]byte, 256), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(path, "missing"), filepath.Join(path, "iommu_group")); err != nil {
		t.Fatal(err)
	}
	m = pcie.PCIeMeasurement{Device: pcie.PCIDevice{BDF: "0000:65:00.0"}}
	collectPCIIsolation(context.Background(), path, &m)
	if m.Device.ACS != nil || m.Device.IOMMU != nil || len(m.Errors()) != 2 {
		t.Fatalf("unavailable isolation metadata: %+v, errors=%v", m.Device, m.Errors())
	}
	var failed []string
	for _, failure := range m.Errors() {
		failed = append(failed, failure.FailedSignals()...)
	}
	if !slices.Contains(failed, pcie.SignalACSSupported) || !slices.Contains(failed, pcie.SignalIOMMUGroup) || slices.Contains(failed, pcie.SignalVendorID) {
		t.Fatalf("incorrect failed signals: %v", failed)
	}
}

func TestPCIACSInvalidCapabilities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	for _, header := range []uint32{0x10010001, 0x10110001, 0x08010001, 0x0000000d, 0xffffffff} {
		config := make([]byte, 4096)
		binary.LittleEndian.PutUint32(config[0x100:], header)
		if err := os.WriteFile(path, config, 0o600); err != nil {
			t.Fatal(err)
		}
		if acs, err := readPCIACS(context.Background(), path); err == nil || acs != nil {
			t.Errorf("accepted invalid header %#x: %+v, error=%v", header, acs, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readPCIACS(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("ACS cancellation: %v", err)
	}
	if _, err := readPCIIOMMU(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("IOMMU cancellation: %v", err)
	}
	if _, err := readPCIACS(context.Background(), filepath.Join(path, "missing")); !errors.Is(err, health.ErrSignalUnavailable) {
		t.Fatalf("missing configuration space: %v", err)
	}
}
