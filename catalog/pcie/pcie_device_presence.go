// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// DevicePresence verifies that every configured BDF appears in the node inventory.
func DevicePresence() health.CheckDefinition {
	check := baseCheck("pcie_device_presence", "PCIe Device Presence",
		"Verifies every configured full BDF is present in the observed inventory; additional devices are allowed.", SignalDeviceBDFs)
	check.Thresholds = map[string]health.ThresholdSpec{
		"expected_device_bdfs": {
			Value: []string{}, Category: health.DefaultThreshold, RequiredOverride: true,
			Unit: "BDF", Description: "Non-empty list of full BDFs expected on this node.",
		},
	}
	check.Rules = []health.Rule{
		{
			Expression: "size(expected_device_bdfs) == 0 || expected_device_bdfs.exists(bdf, bdf == '')", Severity: health.Unknown,
			Message: "PCIe device presence requires a non-empty list of non-empty expected BDFs.",
		},
		{
			Expression:        "size(expected_device_bdfs) > 0 && expected_device_bdfs.exists(bdf, bdf != '' && !(bdf in pci_device_bdfs))",
			Severity:          health.Critical,
			RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
			Message:           "Expected PCI BDFs ${expected_device_bdfs}; observed ${pci_device_bdfs}. One or more devices are missing.",
		},
	}
	return check
}
