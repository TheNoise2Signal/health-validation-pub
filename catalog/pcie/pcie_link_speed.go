// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// LinkSpeed compares negotiated speed with operator minima per BDF.
func LinkSpeed() health.CheckDefinition {
	check := baseCheck("pcie_link_speed", "PCIe Link Speed",
		"Verifies negotiated PCIe speed against configured per-device minimum speeds. Measure in the intended power/workload state.", SignalLinkBDF, SignalLinkSpeed)
	check.SchedulingCadence = health.Prolog
	check.Thresholds = map[string]health.ThresholdSpec{
		"min_link_speed_gt_per_sec_by_bdf": {
			Value: map[string]float64{}, Category: health.DefaultThreshold, RequiredOverride: true,
			Description: "Minimum negotiated speed in GT/s keyed by full BDF, appropriate to the measurement power/workload state.", Unit: "GT/s",
		},
	}
	check.Rules = []health.Rule{
		{
			Expression: "!(pcie_link_bdf in min_link_speed_gt_per_sec_by_bdf)", Severity: health.Unknown,
			Message: "PCIe ${pcie_link_bdf}: minimum link speed is not configured.",
		},
		{
			Expression: "pcie_link_bdf in min_link_speed_gt_per_sec_by_bdf && min_link_speed_gt_per_sec_by_bdf[pcie_link_bdf] <= 0.0", Severity: health.Unknown,
			Message: "PCIe ${pcie_link_bdf}: configured minimum speed must be positive.",
		},
		{Expression: "pcie_link_speed_gt_per_sec < 0.0", Severity: health.Unknown, Message: "PCIe ${pcie_link_bdf}: invalid negative link speed."},
		{
			Expression:        "pcie_link_bdf in min_link_speed_gt_per_sec_by_bdf && min_link_speed_gt_per_sec_by_bdf[pcie_link_bdf] > 0.0 && pcie_link_speed_gt_per_sec >= 0.0 && pcie_link_speed_gt_per_sec < min_link_speed_gt_per_sec_by_bdf[pcie_link_bdf]",
			Severity:          health.Critical,
			RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
			Message:           "PCIe ${pcie_link_bdf}: negotiated speed ${pcie_link_speed_gt_per_sec} GT/s is below the configured minimum.",
		},
	}
	return check
}
