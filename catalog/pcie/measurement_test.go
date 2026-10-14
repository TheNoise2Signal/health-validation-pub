// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func TestPCIeMeasurementEvaluation(t *testing.T) {
	m := pcie.PCIeMeasurement{Device: pcie.PCIDevice{
		BDF: "0000:65:00.0", VendorID: 0x10de, Type: "gpu",
	}}
	if err := catalog.CheckDerivedSignals(m); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CheckDerivedSignalKeysStable(m); err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	cat.MustRegister(health.CheckDefinition{
		ID: "test_pci_identity", Category: health.PCIe,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		SchedulingCadence: health.Continuous,
		RequiredSignals:   []string{pcie.SignalVendorID, pcie.SignalDeviceType},
		Rules: []health.Rule{{
			Expression: `vendor_id == 4318 && device_type == "gpu"`,
			Severity:   health.Info, Message: "Found ${bdf}",
		}},
	})
	verdict, err := cat.EvaluateAllWithOptions(catalog.EvaluationOptions{}, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdict.CheckResults) != 1 || verdict.CheckResults[0].Severity != health.Info {
		t.Fatalf("unexpected verdict: %+v", verdict)
	}
	if !slices.Equal(verdict.CheckResults[0].Messages, []string{"Found 0000:65:00.0"}) {
		t.Errorf("messages = %v", verdict.CheckResults[0].Messages)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var device pcie.PCIDevice
	if err = json.Unmarshal(data, &device); err != nil || device != m.Device {
		t.Fatalf("inventory JSON: %s, error=%v", data, err)
	}
	failed := pcie.ErroredPCIe(errors.New("discovery failed"))
	if len(failed.Errors()) != 1 || len(failed.Errors()[0].FailedSignals()) != len(pcie.MeasurementSignals) {
		t.Fatalf("incomplete discovery error: %+v", failed.Errors())
	}
}

func TestPCIeIsolationSignalsAndJSON(t *testing.T) {
	m := pcie.PCIeMeasurement{Device: pcie.PCIDevice{
		BDF:   testBDF,
		IOMMU: &pcie.IOMMUInfo{GroupID: 0, Devices: []string{testBDF}},
		ACS:   &pcie.ACSInfo{Supported: true, Capabilities: 0x7f, Control: 0x0c},
	}}
	if err := catalog.CheckDerivedSignals(m); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CheckDerivedSignalKeysStable(m); err != nil {
		t.Fatal(err)
	}
	signals := m.DerivedSignals()
	if signals[pcie.SignalIOMMUGroup] != int64(0) || signals[pcie.SignalACSSupported] != true || signals[pcie.SignalACSCapabilities] != int64(0x7f) || signals[pcie.SignalACSControl] != int64(0x0c) {
		t.Fatalf("isolation signals: %v", signals)
	}
	if !slices.Equal(signals[pcie.SignalIOMMUGroupDevices].([]string), []string{testBDF}) {
		t.Fatalf("group members: %v", signals)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var device pcie.PCIDevice
	if err = json.Unmarshal(data, &device); err != nil {
		t.Fatal(err)
	}
	if device.ACS == nil || *device.ACS != *m.Device.ACS || device.IOMMU == nil || device.IOMMU.GroupID != 0 || !slices.Equal(device.IOMMU.Devices, []string{testBDF}) {
		t.Fatalf("isolation JSON: %s", data)
	}
	data, err = json.Marshal(pcie.PCIeMeasurement{})
	if err != nil {
		t.Fatal(err)
	}
	var unknown map[string]any
	if err = json.Unmarshal(data, &unknown); err != nil {
		t.Fatal(err)
	}
	if unknown["acs"] != nil || unknown["iommu"] != nil {
		t.Fatalf("unknown metadata must be null: %s", data)
	}
}

func TestPCIPropertiesCEL(t *testing.T) {
	currentGeneration, maxGeneration, maxWidth, node := int64(5), int64(6), int64(16), int64(0)
	maxSpeed := float64(64)
	driver, cpus, physicalFunction := "example-driver", "0-3", ""
	totalVFs, enabledVFs := int64(8), int64(1)
	m := pcie.PCIeMeasurement{Device: pcie.PCIDevice{
		BDF:      testBDF,
		Link:     &pcie.PCILinkInfo{CurrentGeneration: &currentGeneration, MaxGeneration: &maxGeneration, MaxWidth: &maxWidth, MaxSpeedGTPerSec: &maxSpeed},
		Locality: &pcie.PCILocalityInfo{NUMANode: &node, LocalCPUs: &cpus},
		Driver:   &driver,
		SRIOV:    &pcie.PCISRIOVInfo{PhysicalFunctionBDF: &physicalFunction, TotalVFs: &totalVFs, EnabledVFs: &enabledVFs, VirtualFunctionBDFs: []string{"0000:65:00.1"}},
	}}
	if err := catalog.CheckDerivedSignalKeysStable(m); err != nil {
		t.Fatal(err)
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	cat.MustRegister(health.CheckDefinition{
		ID: "test_pci_properties", Category: health.PCIe,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		SchedulingCadence: health.Continuous,
		RequiredSignals: []string{
			pcie.SignalPCICurrentGeneration, pcie.SignalPCIMaxGeneration, pcie.SignalPCIMaxLinkSpeed, pcie.SignalPCIMaxLinkWidth,
			pcie.SignalPCINUMANode, pcie.SignalPCILocalCPUs, pcie.SignalPCIDriver,
			pcie.SignalPCIPhysicalFunction, pcie.SignalPCITotalVFs, pcie.SignalPCIEnabledVFs, pcie.SignalPCIVirtualFunctions,
		},
		Rules: []health.Rule{{
			Expression: `pci_current_generation == 5 && pci_max_generation == 6 && pci_max_link_speed_gt_per_sec == 64.0 && pci_max_link_width == 16 && pci_numa_node == 0 && pci_local_cpus == "0-3" && pci_driver == "example-driver" && pci_physical_function_bdf == "" && pci_total_vfs == 8 && pci_enabled_vfs == 1 && "0000:65:00.1" in pci_virtual_function_bdfs`,
			Severity:   health.Info, Message: "PCI ${bdf} properties are available",
		}},
	})
	verdict, err := cat.EvaluateAll(m)
	if err != nil || len(verdict.CheckResults) != 1 || verdict.CheckResults[0].Severity != health.Info || verdict.CheckResults[0].Skipped {
		t.Fatalf("PCI property evaluation: %+v, error=%v", verdict, err)
	}
}

func TestPCIeAffectedEntities(t *testing.T) {
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra), catalog.WithOverrides(catalog.NewOverrideSet(
		pciOverride("pcie_link_width", "expected_link_width_by_bdf", map[string]int64{testBDF: 16}),
		pciOverride("pcie_link_speed", "min_link_speed_gt_per_sec_by_bdf", map[string]float64{testBDF: 32}),
	)))
	for _, check := range []health.CheckDefinition{pcie.LinkWidth(), pcie.LinkSpeed(), pcie.ErrorCounters()} {
		cat.MustRegister(check)
	}
	for _, scenario := range []string{"healthy", "fault", "collection_failure"} {
		t.Run(scenario, func(t *testing.T) {
			link := pcie.LinkMeasurement{BDF: testBDF, Width: 16, SpeedGTPerSec: 32}
			counters := pcie.ErrorMeasurement{BDF: testBDF}
			var expected []string
			if scenario != "healthy" {
				expected = []string{testBDF}
			}
			switch scenario {
			case "fault":
				link.Width, link.SpeedGTPerSec, counters.FatalErrors = 8, 16, 1
			case "collection_failure":
				link.AddError(errors.New("link read failed"), pcie.SignalLinkSpeed, pcie.SignalLinkWidth)
				counters.AddError(errors.New("AER read failed"), pcie.SignalFatalErrors)
			}
			verdict, err := cat.EvaluateAll(link, counters)
			if err != nil {
				t.Fatal(err)
			}
			if len(verdict.CheckResults) != 3 {
				t.Fatalf("check count=%d, want 3", len(verdict.CheckResults))
			}
			for _, check := range verdict.CheckResults {
				if !slices.Equal(check.AffectedEntities, expected) {
					t.Errorf("%s affected entities=%v, want %v", check.CheckID, check.AffectedEntities, expected)
				}
			}
			if scenario == "healthy" {
				if len(verdict.AffectedComponents) != 0 {
					t.Fatalf("healthy affected components: %+v", verdict.AffectedComponents)
				}
			} else if len(verdict.AffectedComponents) != 1 || !slices.Equal(verdict.AffectedComponents[0].Entities, []string{testBDF, testBDF, testBDF}) {
				t.Fatalf("affected components: %+v", verdict.AffectedComponents)
			}
		})
	}
}
