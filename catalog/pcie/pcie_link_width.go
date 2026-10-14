// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// LinkWidth compares negotiated lane counts with operator expectations per BDF.
func LinkWidth() health.CheckDefinition {
	check := baseCheck("pcie_link_width", "PCIe Link Width",
		"Verifies negotiated PCIe lane counts against configured per-device widths.", SignalLinkBDF, SignalLinkWidth)
	check.Thresholds = map[string]health.ThresholdSpec{
		"expected_link_width_by_bdf": {
			Value: map[string]int64{}, Category: health.DefaultThreshold, RequiredOverride: true,
			Description: "Expected negotiated lane count keyed by full BDF. Supply the platform wiring expectations.", Unit: "lanes",
		},
	}
	check.Rules = []health.Rule{
		{
			Expression: "!(pcie_link_bdf in expected_link_width_by_bdf)", Severity: health.Unknown,
			Message: "PCIe ${pcie_link_bdf}: expected link width is not configured.",
		},
		{
			Expression: "pcie_link_bdf in expected_link_width_by_bdf && expected_link_width_by_bdf[pcie_link_bdf] <= 0", Severity: health.Unknown,
			Message: "PCIe ${pcie_link_bdf}: configured link width must be positive.",
		},
		{Expression: "pcie_link_width < 0", Severity: health.Unknown, Message: "PCIe ${pcie_link_bdf}: invalid negative link width."},
		{
			Expression:        "pcie_link_bdf in expected_link_width_by_bdf && expected_link_width_by_bdf[pcie_link_bdf] > 0 && pcie_link_width >= 0 && pcie_link_width != expected_link_width_by_bdf[pcie_link_bdf]",
			Severity:          health.Critical,
			RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
			Message:           "PCIe ${pcie_link_bdf}: negotiated width is x${pcie_link_width}; it does not match the configured width.",
		},
	}
	return check
}
