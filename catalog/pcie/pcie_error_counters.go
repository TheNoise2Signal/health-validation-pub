// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// ErrorCounters evaluates cumulative AER totals without inferring an error rate.
func ErrorCounters() health.CheckDefinition {
	check := baseCheck("pcie_error_counters", "PCIe Error Counters",
		"Evaluates cumulative correctable, nonfatal, and fatal PCIe AER counts since their last reset.", SignalErrorBDF, SignalCorrectableErrors, SignalNonfatalErrors, SignalFatalErrors)
	check.Thresholds = map[string]health.ThresholdSpec{
		"max_correctable_errors": {Value: int64(0), Category: health.DefaultThreshold, Unit: "count", Description: "Allowed cumulative correctable errors; exceeding the budget is WARNING."},
		"max_nonfatal_errors":    {Value: int64(0), Category: health.DefaultThreshold, Unit: "count", Description: "Allowed cumulative nonfatal uncorrectable errors; exceeding the budget is CRITICAL."},
		"max_fatal_errors":       {Value: int64(0), Category: health.DefaultThreshold, Unit: "count", Description: "Allowed cumulative fatal errors; exceeding the budget is CRITICAL."},
	}
	check.Rules = []health.Rule{
		{
			Expression: "pcie_correctable_errors < 0 || pcie_nonfatal_errors < 0 || pcie_fatal_errors < 0 || max_correctable_errors < 0 || max_nonfatal_errors < 0 || max_fatal_errors < 0", Severity: health.Unknown,
			Message: "PCIe ${pcie_error_bdf}: error counts and budgets must be nonnegative.",
		},
		{
			Expression:        "pcie_fatal_errors >= 0 && max_fatal_errors >= 0 && pcie_fatal_errors > max_fatal_errors",
			Severity:          health.Critical,
			RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
			Message:           "PCIe ${pcie_error_bdf}: ${pcie_fatal_errors} fatal errors exceed ${max_fatal_errors}.",
		},
		{
			Expression:        "pcie_nonfatal_errors >= 0 && max_nonfatal_errors >= 0 && pcie_nonfatal_errors > max_nonfatal_errors",
			Severity:          health.Critical,
			RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
			Message:           "PCIe ${pcie_error_bdf}: ${pcie_nonfatal_errors} nonfatal errors exceed ${max_nonfatal_errors}.",
		},
		{
			Expression:        "pcie_correctable_errors >= 0 && max_correctable_errors >= 0 && pcie_correctable_errors > max_correctable_errors",
			Severity:          health.Warning,
			RecommendedAction: health.Monitor,
			Message:           "PCIe ${pcie_error_bdf}: ${pcie_correctable_errors} correctable errors exceed ${max_correctable_errors}.",
		},
	}
	return check
}
