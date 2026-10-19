// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

// withVersionFile points the proc read at a file holding contents, or at a
// missing path when contents is empty.
func withVersionFile(t *testing.T, contents string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "version")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	orig := nvidiaVersionFile
	nvidiaVersionFile = path
	t.Cleanup(func() { nvidiaVersionFile = orig })
}

func TestDriverVersionSensor(t *testing.T) {
	const proc = "NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  575.51.03  Release Build\n"
	nvmlOK := func() (string, error) { return "580.65.06", nil }
	nvmlFails := func() (string, error) { return "", errors.New("nvml unavailable") }

	tests := []struct {
		fromNVML func() (string, error)
		name     string
		procFile string
		want     string
		errored  bool
	}{
		{name: "proc wins", procFile: proc, fromNVML: nvmlOK, want: "575.51.03"},
		{name: "nvml fallback", fromNVML: nvmlOK, want: "580.65.06"},
		{name: "neither", fromNVML: nvmlFails, errored: true},
		{name: "no fallback", errored: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withVersionFile(t, tt.procFile)
			out, err := NewDriverVersionSensor(tt.fromNVML).Measure(context.Background())
			if err != nil {
				t.Fatalf("Measure: %v", err)
			}
			if len(out) != 1 {
				t.Fatalf("Measure returned %d measurements, want 1", len(out))
			}
			m, ok := out[0].(node.DriverVersionMeasurement)
			if !ok {
				t.Fatalf("Measure returned %T, want node.DriverVersionMeasurement", out[0])
			}
			if got := len(m.Errors()) > 0; got != tt.errored {
				t.Errorf("errored = %v, want %v", got, tt.errored)
			}
			if m.Version != tt.want {
				t.Errorf("Version = %q, want %q", m.Version, tt.want)
			}
		})
	}
}
