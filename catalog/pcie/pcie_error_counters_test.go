// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie_test

import (
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func TestPCIeErrorCounters(t *testing.T) {
	service := health.Drain(health.RemediationActionServiceRequired)
	budgets := []catalog.Override{
		pciOverride("pcie_error_counters", "max_correctable_errors", int64(5)),
		pciOverride("pcie_error_counters", "max_nonfatal_errors", int64(2)),
		pciOverride("pcie_error_counters", "max_fatal_errors", int64(1)),
	}
	failed := pcie.ErrorMeasurement{BDF: testBDF, CorrectableErrors: 6, NonfatalErrors: 2, FatalErrors: 2}
	failed.AddError(errors.New("AER unavailable"), pcie.SignalFatalErrors)
	runPCIeCases(t, "pcie_error_counters", map[string]pciCheckCase{
		"zero counters": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF},
			wantSeverity: health.Pass,
		},
		"correctable errors": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF, CorrectableErrors: 1},
			wantAction:   health.Monitor,
			wantSeverity: health.Warning,
			wantMessages: 1,
		},
		"nonfatal errors": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF, NonfatalErrors: 1},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"fatal errors": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF, FatalErrors: 1},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"at configured budgets": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF, CorrectableErrors: 5, NonfatalErrors: 2, FatalErrors: 1},
			overrides:    budgets,
			wantSeverity: health.Pass,
		},
		"above correctable budget": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF, CorrectableErrors: 6, NonfatalErrors: 2, FatalErrors: 1},
			overrides:    budgets,
			wantAction:   health.Monitor,
			wantSeverity: health.Warning,
			wantMessages: 1,
		},
		"multiple exceeded budgets": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF, CorrectableErrors: 6, NonfatalErrors: 2, FatalErrors: 2},
			overrides:    budgets,
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 2,
		},
		"negative counter": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF, CorrectableErrors: -1},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"negative budget": {
			measurement:  pcie.ErrorMeasurement{BDF: testBDF},
			overrides:    []catalog.Override{pciOverride("pcie_error_counters", "max_fatal_errors", int64(-1))},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"collection failure precedes fault evaluation": {
			measurement:  failed,
			overrides:    budgets,
			wantAction:   health.Drain(health.RemediationActionCheckCollection),
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
	})
}
