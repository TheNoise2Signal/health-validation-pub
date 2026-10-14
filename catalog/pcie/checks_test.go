// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie_test

import (
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

const testBDF = "0000:65:00.0"

func pciOverride(id, name string, value any) catalog.Override {
	return catalog.Override{CheckID: id, ThresholdName: name, Value: value}
}

func evaluatePCIe(t *testing.T, id string, m health.Measurement, overrides ...catalog.Override) health.CheckVerdict {
	t.Helper()
	cat := catalog.New(
		catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra),
		catalog.WithOverrides(catalog.NewOverrideSet(overrides...)),
	)
	verdict, err := cat.Evaluate(id, m)
	if err != nil {
		t.Fatal(err)
	}
	return verdict
}

func requireSeverity(t *testing.T, verdict health.CheckVerdict, want health.Severity) {
	t.Helper()
	if verdict.Skipped || verdict.Severity != want {
		t.Fatalf("verdict=%+v, want severity %v", verdict, want)
	}
}

type pciCheckCase struct {
	measurement  health.Measurement
	overrides    []catalog.Override
	wantAction   health.RecommendedAction
	wantSeverity health.Severity
	wantMessages int
}

func runPCIeCases(t *testing.T, id string, cases map[string]pciCheckCase, defaults ...catalog.Override) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			overrides := tc.overrides
			if overrides == nil {
				overrides = defaults
			}
			v := evaluatePCIe(t, id, tc.measurement, overrides...)
			requireSeverity(t, v, tc.wantSeverity)
			if !v.RecommendedAction.Equal(tc.wantAction) {
				t.Errorf("action=%v, want %v", v.RecommendedAction, tc.wantAction)
			}
			if len(v.Messages) != tc.wantMessages {
				t.Errorf("messages=%v, want %d messages", v.Messages, tc.wantMessages)
			}
		})
	}
}

func TestPCIeCheckMetadata(t *testing.T) {
	for _, tc := range []struct {
		check   health.CheckDefinition
		cadence health.SchedulingCadence
	}{
		{check: pcie.LinkSpeed(), cadence: health.Prolog},
		{check: pcie.LinkWidth(), cadence: health.Continuous},
		{check: pcie.DevicePresence(), cadence: health.Continuous},
		{check: pcie.ErrorCounters(), cadence: health.Continuous},
	} {
		t.Run(tc.check.ID, func(t *testing.T) {
			if tc.check.Provenance != health.NVIDIA || tc.check.MaturityLevel != health.Experimental {
				t.Errorf("unexpected provenance or maturity: %+v", tc.check)
			}
			if tc.check.Category != health.PCIe || tc.check.SchedulingCadence != tc.cadence {
				t.Errorf("unexpected category or cadence: %+v", tc.check)
			}
			if len(tc.check.ExecutionContexts) != 1 || tc.check.ExecutionContexts[0] != health.OperatorInfra {
				t.Errorf("unexpected execution contexts: %v", tc.check.ExecutionContexts)
			}
		})
	}
}

func TestPCIeUnconfiguredAndRouting(t *testing.T) {
	for _, id := range []string{"pcie_link_speed", "pcie_link_width", "pcie_device_presence"} {
		v := evaluatePCIe(t, id, pcie.LinkMeasurement{BDF: testBDF, Width: 16, SpeedGTPerSec: 16})
		if !v.Skipped || v.SkipReason != health.ThresholdUnconfigured {
			t.Errorf("%s: %+v", id, v)
		}
	}
	cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra), catalog.WithOverrides(catalog.NewOverrideSet(
		pciOverride("pcie_link_width", "expected_link_width_by_bdf", map[string]int64{testBDF: 16}),
		pciOverride("pcie_link_speed", "min_link_speed_gt_per_sec_by_bdf", map[string]float64{testBDF: 16}),
		pciOverride("pcie_device_presence", "expected_device_bdfs", []string{testBDF}),
	)))
	verdict, err := cat.EvaluateAll(
		pcie.PCIeMeasurement{Device: pcie.PCIDevice{BDF: testBDF}},
		pcie.LinkMeasurement{BDF: testBDF, Width: 16, SpeedGTPerSec: 16},
		pcie.ErrorMeasurement{BDF: testBDF},
		pcie.InventoryMeasurement{BDFs: []string{testBDF}},
	)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, v := range verdict.CheckResults {
		if strings.HasPrefix(v.CheckID, "pcie_") {
			count++
			requireSeverity(t, v, health.Pass)
		}
	}
	if count != 4 {
		t.Fatalf("PCIe result count = %d, want 4", count)
	}
	cadence := health.Continuous
	verdict, err = cat.EvaluateAllWithOptions(catalog.EvaluationOptions{Cadence: &cadence}, pcie.LinkMeasurement{BDF: testBDF, Width: 16, SpeedGTPerSec: 16})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range verdict.SkippedChecks {
		if v.CheckID == "pcie_link_speed" {
			found = true
			if v.SkipReason != health.CadenceMismatch {
				t.Fatalf("speed cadence skip: %+v", v)
			}
		}
	}
	if !found {
		t.Fatal("missing speed cadence skip")
	}
}
