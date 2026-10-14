// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
)

type pcieBackendFunc func(context.Context) ([]health.Measurement, error)

func (f pcieBackendFunc) MeasurePCIe(ctx context.Context) ([]health.Measurement, error) {
	return f(ctx)
}

func TestWithPCIeBackendReachesCLIVerdict(t *testing.T) {
	const bdf = "0000:65:00.0"
	configPath := filepath.Join(t.TempDir(), "cluster.yaml")
	config := []byte(`checks:
  pcie_link_width:
    expected_link_width_by_bdf:
      "0000:65:00.0": 16
  pcie_link_speed:
    min_link_speed_gt_per_sec_by_bdf:
      "0000:65:00.0": 32.0
  pcie_device_presence:
    expected_device_bdfs:
      - "0000:65:00.0"
`)
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"healthy", "degraded_width", "correctable_errors", "missing_device", "collection_failure", "continuous_cadence"} {
		t.Run(scenario, func(t *testing.T) {
			link := pcie.LinkMeasurement{BDF: bdf, Width: 16, SpeedGTPerSec: 32}
			counters := pcie.ErrorMeasurement{BDF: bdf}
			inventory := pcie.InventoryMeasurement{BDFs: []string{bdf}}
			wantCode := exitOK
			if scenario == "degraded_width" {
				link.Width = 8
				wantCode = exitUnhealthy
			} else if scenario == "correctable_errors" {
				counters.CorrectableErrors = 1
			} else if scenario == "missing_device" {
				inventory.BDFs = nil
				wantCode = exitUnhealthy
			} else if scenario == "collection_failure" {
				wantCode = exitUnhealthy
			}
			calls := 0
			backend := pcieBackendFunc(func(ctx context.Context) ([]health.Measurement, error) {
				calls++
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if scenario == "collection_failure" {
					return nil, errors.New("PCI discovery unavailable")
				}
				return []health.Measurement{
					pcie.PCIeMeasurement{Device: pcie.PCIDevice{BDF: bdf}}, link, counters, inventory,
				}, nil
			})
			a := newTestApp().WithNodeID(testNodeID).WithPCIeBackend(backend)
			a.host = fakeHost{b: spyBackend{called: new(bool)}}
			a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}))
			a.WithXidBackend(xidSpyBackend{called: new(bool)})
			a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
			args := []string{"run", "--config", configPath, "--execution-context", health.OperatorInfra.String()}
			if scenario == "continuous_cadence" {
				args = append(args, "--cadence", "CONTINUOUS")
			}
			var stdout, stderr bytes.Buffer
			if code := a.Run(args, &stdout, &stderr); code != wantCode {
				t.Fatalf("exit=%d, want %d; stderr=%s", code, wantCode, stderr.String())
			}
			if calls != 1 {
				t.Fatalf("PCIe backend calls=%d, want 1", calls)
			}
			var verdict health.NodeVerdict
			if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, check := range verdict.CheckResults {
				if !strings.HasPrefix(check.CheckID, "pcie_") {
					continue
				}
				count++
				want := health.Pass
				if scenario == "collection_failure" || scenario == "degraded_width" && check.CheckID == "pcie_link_width" || scenario == "missing_device" && check.CheckID == "pcie_device_presence" {
					want = health.Critical
				} else if scenario == "correctable_errors" && check.CheckID == "pcie_error_counters" {
					want = health.Warning
				}
				if check.Severity != want {
					t.Errorf("%s severity=%v, want %v", check.CheckID, check.Severity, want)
				}
			}
			wantCount := 4
			if scenario == "continuous_cadence" {
				wantCount = 3
				found := false
				for _, check := range verdict.SkippedChecks {
					if check.CheckID == "pcie_link_speed" && check.SkipReason == health.CadenceMismatch {
						found = true
					}
				}
				if !found {
					t.Error("missing PCIe speed cadence skip")
				}
			}
			if count != wantCount {
				t.Errorf("PCIe result count=%d, want %d", count, wantCount)
			}
		})
	}
}

func TestPCIeCollectCommand(t *testing.T) {
	const gpuBDF = "0000:65:00.0"
	const nicBDF = "0000:66:00.0"
	for _, scenario := range []string{"default", "selected", "excluded", "partial", "discovery_failure", "empty", "without_aer"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			gpuCalled := false
			a := newTestApp().WithNodeID(testNodeID)
			a.host = fakeHost{b: spyBackend{called: &gpuCalled}}
			a.WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 1}))
			a.WithXidBackend(xidSpyBackend{called: new(bool)})
			a.WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap())
			a.WithPCIeBackend(pcieBackendFunc(func(ctx context.Context) ([]health.Measurement, error) {
				calls++
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if scenario == "discovery_failure" {
					return nil, errors.New("PCI discovery failed")
				}
				if scenario == "empty" {
					return []health.Measurement{pcie.InventoryMeasurement{BDFs: []string{}}}, nil
				}
				device := pcie.PCIeMeasurement{Device: pcie.PCIDevice{
					BDF: gpuBDF, ParentBDF: "0000:64:00.0", Type: "gpu", VendorID: 0x10de,
					IOMMU: &pcie.IOMMUInfo{GroupID: 3, Devices: []string{gpuBDF}},
					ACS:   &pcie.ACSInfo{Supported: true, Capabilities: 7, Control: 7},
				}}
				link := pcie.LinkMeasurement{BDF: gpuBDF, SpeedGTPerSec: 32, Width: 16}
				if scenario == "partial" {
					device.Device.ACS = nil
					device.AddError(errors.New("ACS unavailable"), pcie.SignalACSSupported, pcie.SignalACSCapabilities, pcie.SignalACSControl)
					link.AddError(errors.New("width unavailable"), pcie.SignalLinkWidth)
				}
				measurements := []health.Measurement{
					device, link,
					pcie.PCIeMeasurement{Device: pcie.PCIDevice{BDF: nicBDF, Type: "nic"}},
					pcie.LinkMeasurement{BDF: nicBDF, SpeedGTPerSec: 16, Width: 8},
					pcie.InventoryMeasurement{BDFs: []string{gpuBDF, nicBDF}},
				}
				if scenario != "without_aer" {
					measurements = append(measurements,
						pcie.ErrorMeasurement{BDF: gpuBDF},
						pcie.ErrorMeasurement{BDF: nicBDF, CorrectableErrors: 2},
					)
				}
				return measurements, nil
			}))
			args := []string{"collect"}
			if scenario != "default" {
				check := "pcie_link_width"
				if scenario == "excluded" {
					check = "gpu_count"
				}
				configPath := filepath.Join(t.TempDir(), "collect.yaml")
				if err := os.WriteFile(configPath, []byte("checks:\n  "+check+": {}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--config", configPath)
			}
			var stdout, stderr bytes.Buffer
			if code := a.Run(args, &stdout, &stderr); code != exitOK {
				t.Fatalf("collect exit=%d; stderr=%s", code, stderr.String())
			}
			var batch observation.ObservationBatch
			if err := protojson.Unmarshal(stdout.Bytes(), &batch); err != nil {
				t.Fatal(err)
			}
			if err := observation.ValidateBatch(&batch); err != nil {
				t.Fatal(err)
			}
			devices := make(map[string]map[string]*observation.Observation)
			var inventory *observation.Observation
			for _, current := range batch.GetObservations() {
				if !strings.HasPrefix(current.GetSignalId(), "pcie.") {
					continue
				}
				if current.GetSignalId() == "pcie.device_bdfs" {
					inventory = current
					continue
				}
				pci := current.GetTarget().GetPcie()
				bdf := pci.GetFields()["bdf"].GetStringValue()
				if scenario != "discovery_failure" && (bdf == "" || pci.GetFields()["nodeId"].GetStringValue() != testNodeID) {
					t.Fatalf("missing PCIe target identity: %v", current)
				}
				if current.GetSource() != "toolkit.pcie" || current.GetObservedAt() == nil {
					t.Fatalf("missing collection context: %v", current)
				}
				if devices[bdf] == nil {
					devices[bdf] = make(map[string]*observation.Observation)
				}
				if devices[bdf][current.GetSignalId()] != nil {
					t.Fatalf("duplicate PCI property: %s %s", bdf, current.GetSignalId())
				}
				devices[bdf][current.GetSignalId()] = current
			}
			if scenario == "excluded" {
				if calls != 0 || len(devices) != 0 || inventory != nil {
					t.Fatalf("unselected PCI sensor ran: calls=%d", calls)
				}
				return
			}
			if calls != 1 || scenario != "default" && gpuCalled || inventory == nil {
				t.Fatalf("sensor selection: calls=%d, GPU called=%v, inventory=%v", calls, gpuCalled, inventory)
			}
			if inventory.GetTarget().GetNode().GetId() != testNodeID {
				t.Fatalf("inventory target is not the node: %v", inventory)
			}
			if scenario == "discovery_failure" {
				if inventory.GetCollectionError() == nil || !strings.Contains(inventory.GetCollectionError().GetDetail(), "PCI discovery failed") {
					t.Fatalf("discovery failure was lost: %v", inventory)
				}
				return
			}
			if scenario == "empty" {
				if len(devices) != 0 || inventory.GetValue().GetStringValue() != "[]" {
					t.Fatalf("empty PCI inventory: %v", inventory)
				}
				return
			}
			if len(devices) != 2 {
				t.Fatalf("device count=%d, want 2", len(devices))
			}
			width := devices[gpuBDF]["pcie.link_width"]
			properties := width.GetTarget().GetPcie().AsMap()
			if properties["parentBdf"] != "0000:64:00.0" || properties["deviceType"] != "gpu" || properties["vendorId"] != "0x10de" || properties["iommu"].(map[string]any)["groupId"] != float64(3) {
				t.Fatalf("PCI device context was lost: %+v", properties)
			}
			if devices[nicBDF]["pcie.link_width"].GetValue().GetIntValue() != 8 {
				t.Fatal("NIC width was mixed with GPU width")
			}
			if scenario == "partial" {
				if width.GetCollectionError() == nil || properties["acs"] != nil || devices[gpuBDF]["pcie.acs_supported"].GetCollectionError() == nil {
					t.Fatal("per-field PCI errors were lost")
				}
			} else if width.GetValue().GetIntValue() != 16 || properties["acs"] == nil {
				t.Fatal("healthy PCI metadata or width was lost")
			}
			if devices[gpuBDF]["pcie.device_present"].GetValue().GetBoolValue() != true {
				t.Fatal("device presence was lost")
			}
			if scenario == "without_aer" {
				if devices[gpuBDF]["pcie.fatal_errors"] != nil || devices[nicBDF]["pcie.correctable_errors"] != nil {
					t.Fatal("fabricated AER counters for unsupported devices")
				}
			} else if devices[nicBDF]["pcie.correctable_errors"].GetValue().GetIntValue() != 2 {
				t.Fatal("AER counters were lost")
			}
		})
	}
}

func TestPCIeExportWithoutPrebuiltTargets(t *testing.T) {
	maxWidth := int64(16)
	device := pcie.PCIeMeasurement{Device: pcie.PCIDevice{
		BDF: "0000:65:00.0", DeviceID: 0x1234,
		ACS:   &pcie.ACSInfo{Supported: true},
		IOMMU: &pcie.IOMMUInfo{GroupID: 0, Devices: []string{"0000:65:00.0"}},
		Link:  &pcie.PCILinkInfo{MaxWidth: &maxWidth},
	}}
	device.AddError(errors.New("metadata unavailable"), pcie.SignalVendorID, pcie.SignalACSSupported, pcie.SignalIOMMUGroup, pcie.SignalIOMMUGroupDevices, pcie.SignalPCIMaxLinkWidth)
	exported, err := observations.FromMeasurements("pcie", testNodeID, time.Unix(1, 0), &device)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported) != 6 {
		t.Fatalf("observation count=%d, want presence and five errors", len(exported))
	}
	for _, current := range exported {
		properties := current.GetTarget().GetPcie().AsMap()
		if properties["vendorId"] != nil || properties["deviceId"] != "0x1234" || properties["acs"].(map[string]any)["supported"] != nil || properties["iommu"].(map[string]any)["groupId"] != nil {
			t.Fatalf("failed metadata published as known: %+v", properties)
		}
		if properties["iommu"].(map[string]any)["devices"] != nil || properties["link"].(map[string]any)["maxWidth"] != nil {
			t.Fatalf("failed nested metadata published as known: %+v", properties)
		}
		if current.GetSignalId() != "pcie.device_present" && current.GetCollectionError() == nil {
			t.Fatalf("lost metadata error: %v", current)
		}
	}
}
