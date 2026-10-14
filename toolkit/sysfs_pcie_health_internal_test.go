// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func TestSysfsPCIeHealthMeasurements(t *testing.T) {
	root := t.TempDir()
	bdf := "0000:65:00.0"
	path := filepath.Join(root, bdf)
	writePCIFixture(t, path, "0x030200\n")
	backend := &sysfsPCIeBackend{root: root}
	measurements, err := backend.MeasurePCIe(context.Background())
	if err != nil || len(measurements) != 4 {
		t.Fatalf("measurements=%v, error=%v", measurements, err)
	}
	link := measurements[1].(pcie.LinkMeasurement)
	counters := measurements[2].(pcie.ErrorMeasurement)
	inventory := measurements[3].(pcie.InventoryMeasurement)
	if link.BDF != bdf || link.Width != 16 || link.SpeedGTPerSec != 32 || len(link.Errors()) != 0 {
		t.Fatalf("unexpected link: %+v, errors=%v", link, link.Errors())
	}
	if counters.BDF != bdf || counters.CorrectableErrors != 0 || counters.NonfatalErrors != 0 || counters.FatalErrors != 0 || len(counters.Errors()) != 0 {
		t.Fatalf("unexpected counters: %+v, errors=%v", counters, counters.Errors())
	}
	if !slices.Equal(inventory.BDFs, []string{bdf}) || len(inventory.Errors()) != 0 {
		t.Fatalf("unexpected inventory: %+v", inventory)
	}
	// Multiple error-bit rows can belong to one reported AER message.
	if err = os.WriteFile(filepath.Join(path, "aer_dev_correctable"), []byte("Receiver Error 4\nBad TLP 4\nTOTAL_ERR_COR 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(path, "current_link_speed")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "aer_dev_fatal"), []byte("TOTAL_ERR_FATAL -1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	measurements, err = backend.MeasurePCIe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	link = measurements[1].(pcie.LinkMeasurement)
	counters = measurements[2].(pcie.ErrorMeasurement)
	if link.Width != 16 || len(link.Errors()) != 1 || !slices.Equal(link.Errors()[0].FailedSignals(), []string{pcie.SignalLinkSpeed}) {
		t.Fatalf("partial link failure: %+v, errors=%v", link, link.Errors())
	}
	if counters.CorrectableErrors != 4 || len(counters.Errors()) != 1 || !slices.Equal(counters.Errors()[0].FailedSignals(), []string{pcie.SignalFatalErrors}) {
		t.Fatalf("partial AER failure: %+v, errors=%v", counters, counters.Errors())
	}
}

func TestSysfsPCIeUnavailableAttributes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "0000:65:00.0")
	writePCIFixture(t, path, "0x030200\n")
	for _, name := range []string{"current_link_width", "current_link_speed", "aer_dev_correctable", "aer_dev_nonfatal", "aer_dev_fatal"} {
		if err := os.Remove(filepath.Join(path, name)); err != nil {
			t.Fatal(err)
		}
	}
	measurements, err := (&sysfsPCIeBackend{root: root}).MeasurePCIe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 3 || len(measurements[0].Errors()) != 1 || !slices.Equal(measurements[0].Errors()[0].FailedSignals(), []string{pcie.SignalPCICurrentGeneration}) || len(measurements[1].Errors()) != 2 || len(measurements[2].Errors()) != 0 {
		t.Fatalf("unexpected error attribution: %+v", measurements)
	}
}

func TestSysfsPCIeMissingLinkWidth(t *testing.T) {
	root := t.TempDir()
	bdf := "0000:a0:08.0"
	path := filepath.Join(root, bdf)
	writePCIFixture(t, path, "0x060400\n")
	if err := os.Remove(filepath.Join(path, "current_link_width")); err != nil {
		t.Fatal(err)
	}
	measurements, err := (&sysfsPCIeBackend{root: root}).MeasurePCIe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 4 {
		t.Fatalf("measurement count = %d, want 4", len(measurements))
	}
	link := measurements[1].(pcie.LinkMeasurement)
	if link.BDF != bdf || link.SpeedGTPerSec != 32 || len(link.Errors()) != 1 {
		t.Fatalf("unexpected link: %+v, errors=%v", link, link.Errors())
	}
	failure := link.Errors()[0]
	if !errors.Is(failure.Err(), os.ErrNotExist) || !errors.Is(failure.Err(), health.ErrSignalUnavailable) || !slices.Equal(failure.FailedSignals(), []string{pcie.SignalLinkWidth}) {
		t.Fatalf("missing width failure: %v, signals=%v", failure.Err(), failure.FailedSignals())
	}
	for _, i := range []int{0, 2, 3} {
		if len(measurements[i].Errors()) != 0 {
			t.Errorf("measurement %d has unrelated errors: %v", i, measurements[i].Errors())
		}
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra), catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{CheckID: "pcie_link_width", ThresholdName: "expected_link_width_by_bdf", Value: map[string]int64{bdf: 16}},
	)))
	cat.MustRegister(pcie.LinkWidth())
	verdict, err := Collect(cat, NewPCIeSensor(&sysfsPCIeBackend{root: root}))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.OverallSeverity != health.Unknown || len(verdict.CheckResults) != 1 || len(verdict.SkippedChecks) != 0 {
		t.Fatalf("unavailable width verdict: %+v", verdict)
	}
	check := verdict.CheckResults[0]
	if check.Severity != health.Unknown || len(check.Messages) != 1 || !strings.Contains(check.Messages[0], bdf) || !strings.Contains(check.Messages[0], "current_link_width") {
		t.Fatalf("unavailable width detail: %+v", check)
	}
}

func TestPCIeSysfsToCatalog(t *testing.T) {
	root := t.TempDir()
	bdf := "0000:65:00.0"
	path := filepath.Join(root, bdf)
	writePCIFixture(t, path, "0x030200\n")
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra), catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{CheckID: "pcie_link_width", ThresholdName: "expected_link_width_by_bdf", Value: map[string]int64{bdf: 16}},
		catalog.Override{CheckID: "pcie_link_speed", ThresholdName: "min_link_speed_gt_per_sec_by_bdf", Value: map[string]float64{bdf: 32}},
		catalog.Override{CheckID: "pcie_device_presence", ThresholdName: "expected_device_bdfs", Value: []string{bdf}},
	)))
	for _, check := range []health.CheckDefinition{pcie.LinkWidth(), pcie.LinkSpeed(), pcie.ErrorCounters(), pcie.DevicePresence()} {
		cat.MustRegister(check)
	}
	sensor := NewPCIeSensor(&sysfsPCIeBackend{root: root})
	verdict, err := Collect(cat, sensor)
	if err != nil || verdict.OverallSeverity != health.Pass || len(verdict.CheckResults) != 4 || len(verdict.SkippedChecks) != 0 {
		t.Fatalf("healthy fixture: %+v, error=%v", verdict, err)
	}
	for name, value := range map[string]string{
		"current_link_speed":  "16.0 GT/s PCIe\n",
		"current_link_width":  "8\n",
		"aer_dev_correctable": "Receiver Error 1\nTOTAL_ERR_COR 1\n",
	} {
		if err = os.WriteFile(filepath.Join(path, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	verdict, err = Collect(cat, sensor)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range verdict.CheckResults {
		want := health.Critical
		if check.CheckID == "pcie_device_presence" {
			want = health.Pass
		} else if check.CheckID == "pcie_error_counters" {
			want = health.Warning
		}
		if check.CheckID != "pcie_device_presence" && !slices.Equal(check.AffectedEntities, []string{bdf}) {
			t.Errorf("%s affected entities=%v, want %s", check.CheckID, check.AffectedEntities, bdf)
		}
		if check.Severity != want {
			t.Errorf("%s severity=%v, want %v", check.CheckID, check.Severity, want)
		}
	}
	// Successful discovery with no devices still feeds the presence check.
	verdict, err = Collect(cat, NewPCIeSensor(&sysfsPCIeBackend{root: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	if len(verdict.CheckResults) != 1 || verdict.CheckResults[0].CheckID != "pcie_device_presence" || verdict.CheckResults[0].Severity != health.Critical {
		t.Fatalf("empty inventory: %+v", verdict)
	}
	// A hard discovery failure degrades every measurement schema.
	verdict, err = Collect(cat, NewPCIeSensor(&sysfsPCIeBackend{root: filepath.Join(root, "missing")}))
	if err != nil || len(verdict.CheckResults) != 4 || len(verdict.SkippedChecks) != 0 {
		t.Fatalf("discovery failure: %+v, error=%v", verdict, err)
	}
	for _, check := range verdict.CheckResults {
		if check.Severity != health.Critical {
			t.Errorf("%s discovery failure severity=%v", check.CheckID, check.Severity)
		}
	}
}

func TestPCIeHealthParsers(t *testing.T) {
	t.Run("link_speeds", func(t *testing.T) {
		for text, want := range map[string]float64{"2.5 GT/s PCIe\n": 2.5, "16.0 GT/s PCIe\n": 16, "64.0 GT/s PCIe\n": 64, "0 GT/s": 0} {
			got, err := parsePCILinkSpeed([]byte(text))
			if err != nil || got != want {
				t.Errorf("speed %q: value=%v, error=%v", text, got, err)
			}
		}
		for _, text := range []string{"Unknown", "", "16 GB/s", "-1 GT/s", "NaN GT/s", "+Inf GT/s", "1e999 GT/s"} {
			if _, err := parsePCILinkSpeed([]byte(text)); err == nil {
				t.Errorf("invalid speed accepted: %q", text)
			}
		}
	})
	t.Run("counts", func(t *testing.T) {
		for _, text := range []string{"", "-1", "1.5", "0x10", "9223372036854775808"} {
			if _, err := parsePCICount(text); err == nil {
				t.Errorf("invalid count accepted: %q", text)
			}
		}
	})
	t.Run("aer_totals", func(t *testing.T) {
		if got, err := parsePCIAERTotal([]byte("Receiver Error 5\nBad TLP 5\nTOTAL_ERR_COR 5\n"), "TOTAL_ERR_COR"); err != nil || got != 5 {
			t.Fatalf("AER total=%d, error=%v", got, err)
		}
		for _, text := range []string{"Receiver Error 1\n", "TOTAL_ERR_COR -1\n", "TOTAL_ERR_COR 1\nTOTAL_ERR_COR 2\n", "TOTAL_ERR_COR\n", "TOTAL_ERR_COR 1 extra\n", "TOTAL_ERR_COR 9223372036854775808\n"} {
			if _, err := parsePCIAERTotal([]byte(text), "TOTAL_ERR_COR"); err == nil {
				t.Errorf("invalid AER total accepted: %q", text)
			}
		}
	})
}

func TestSysfsPCIeUnsupportedAER(t *testing.T) {
	root := t.TempDir()
	bdf := "0000:65:00.0"
	path := filepath.Join(root, bdf)
	writePCIFixture(t, path, "0x030200\n")
	for _, name := range []string{"aer_dev_correctable", "aer_dev_nonfatal", "aer_dev_fatal"} {
		if err := os.Remove(filepath.Join(path, name)); err != nil {
			t.Fatal(err)
		}
	}
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra), catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{CheckID: "pcie_link_width", ThresholdName: "expected_link_width_by_bdf", Value: map[string]int64{bdf: 16}},
		catalog.Override{CheckID: "pcie_link_speed", ThresholdName: "min_link_speed_gt_per_sec_by_bdf", Value: map[string]float64{bdf: 32}},
		catalog.Override{CheckID: "pcie_device_presence", ThresholdName: "expected_device_bdfs", Value: []string{bdf}},
	)))
	for _, check := range []health.CheckDefinition{pcie.LinkWidth(), pcie.LinkSpeed(), pcie.ErrorCounters(), pcie.DevicePresence()} {
		cat.MustRegister(check)
	}
	verdict, err := Collect(cat, NewPCIeSensor(&sysfsPCIeBackend{root: root}))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.OverallSeverity != health.Pass || len(verdict.CheckResults) != 3 || len(verdict.SkippedChecks) != 1 {
		t.Fatalf("unsupported AER verdict: %+v", verdict)
	}
	if skip := verdict.SkippedChecks[0]; skip.CheckID != "pcie_error_counters" || skip.SkipReason != health.NoApplicableMeasurement {
		t.Fatalf("unsupported AER skip: %+v", skip)
	}
	// Unsupported functions must not hide faults on functions exposing AER.
	peer := "0000:66:00.0"
	peerPath := filepath.Join(root, peer)
	writePCIFixture(t, peerPath, "0x020700\n")
	if err = os.WriteFile(filepath.Join(peerPath, "aer_dev_fatal"), []byte("TOTAL_ERR_FATAL 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cat = catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	cat.MustRegister(pcie.ErrorCounters())
	verdict, err = Collect(cat, NewPCIeSensor(&sysfsPCIeBackend{root: root}))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.OverallSeverity != health.Critical || len(verdict.CheckResults) != 1 || len(verdict.SkippedChecks) != 0 || !slices.Equal(verdict.CheckResults[0].AffectedEntities, []string{peer}) {
		t.Fatalf("mixed AER support verdict: %+v", verdict)
	}
}

func TestSysfsPCIeAERCollectionFailures(t *testing.T) {
	for _, scenario := range []string{"partial", "malformed", "unreadable", "disappeared"} {
		t.Run(scenario, func(t *testing.T) {
			bdf := "0000:65:00.0"
			path := filepath.Join(t.TempDir(), bdf)
			if scenario != "disappeared" {
				writePCIFixture(t, path, "0x030200\n")
				attribute := filepath.Join(path, "aer_dev_fatal")
				if err := os.Remove(attribute); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "malformed":
					if err := os.WriteFile(attribute, []byte("TOTAL_ERR_FATAL -1\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "unreadable":
					if err := os.Mkdir(attribute, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			counters, applicable := collectPCIErrorCounters(context.Background(), path, bdf)
			if !applicable {
				t.Fatal("collection failure was treated as unsupported AER")
			}
			cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
			cat.MustRegister(pcie.ErrorCounters())
			verdict, err := cat.EvaluateAll(counters)
			if err != nil {
				t.Fatal(err)
			}
			if verdict.OverallSeverity != health.Critical || len(verdict.CheckResults) != 1 || !slices.Equal(verdict.CheckResults[0].AffectedEntities, []string{bdf}) {
				t.Fatalf("AER collection failure verdict: %+v", verdict)
			}
		})
	}
}
