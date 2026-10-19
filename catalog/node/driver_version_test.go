// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package node_test

import (
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

func TestDriverVersionMeasurement_Parsed(t *testing.T) {
	got := node.DriverVersionMeasurement{Version: "575.51.03"}.Parsed()
	if !got.AtLeast(575, 0, 0) || got.Raw != "575.51.03" {
		t.Errorf("Parsed() = %+v, want 575.51.03", got)
	}
}

// An errored or unreadable version must read as unknown, never as some
// particular (old) driver.
func TestDriverVersionMeasurement_ParsedUnknown(t *testing.T) {
	for name, m := range map[string]node.DriverVersionMeasurement{
		"errored":    node.ErroredDriverVersion(errors.New("no /proc/driver/nvidia/version")),
		"unreadable": {Version: "not-a-version"},
	} {
		if got := m.Parsed(); got.Known() {
			t.Errorf("%s: Parsed() = %+v, want unknown", name, got)
		}
	}
}
