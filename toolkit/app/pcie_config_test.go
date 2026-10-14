// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
)

func TestPCIeThresholdMapCoercion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pcie.yaml")
	data := []byte(`checks:
  pcie_link_width:
    expected_link_width_by_bdf:
      "0000:65:00.0": 16
  pcie_link_speed:
    min_link_speed_gt_per_sec_by_bdf:
      "0000:65:00.0": 16
      "0001:65:00.0": 2.5
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := mustLoadConfig(t, path)
	overrides, err := cfg.Overrides(catalog.New())
	if err != nil {
		t.Fatal(err)
	}
	if got := overrides.ForCheck("pcie_link_width")["expected_link_width_by_bdf"]; !reflect.DeepEqual(got, map[string]int64{"0000:65:00.0": 16}) {
		t.Errorf("width override = %#v", got)
	}
	if got := overrides.ForCheck("pcie_link_speed")["min_link_speed_gt_per_sec_by_bdf"]; !reflect.DeepEqual(got, map[string]float64{"0000:65:00.0": 16, "0001:65:00.0": 2.5}) {
		t.Errorf("speed override = %#v", got)
	}
	for _, invalid := range []any{"x16", map[string]any{"0000:65:00.0": 2.5}, map[string]any{"0000:65:00.0": "16"}} {
		cfg.Checks["pcie_link_width"]["expected_link_width_by_bdf"] = invalid
		if _, err := cfg.Overrides(catalog.New()); err == nil {
			t.Errorf("invalid width override accepted: %#v", invalid)
		}
	}
	for _, invalid := range []any{"fast", math.NaN(), math.Inf(1), math.Inf(-1)} {
		cfg.Checks = map[string]map[string]any{"pcie_link_speed": {"min_link_speed_gt_per_sec_by_bdf": map[string]any{"0000:65:00.0": invalid}}}
		if _, err := cfg.Overrides(catalog.New()); err == nil {
			t.Errorf("invalid speed override accepted: %v", invalid)
		}
	}
}
