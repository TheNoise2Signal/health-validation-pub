// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// A degraded sensor's placeholder names no GPU, so it is keyed by the sensor
// rather than taking the sensor name as a GPU identity.
func TestDegradedGPUSensorsAreKeyedBySource(t *testing.T) {
	for _, s := range []toolkit.Sensor{
		toolkit.NewGPUSensor(toolkit.MockGPU()),
		toolkit.NewNVLinkSensor(toolkit.MockNVLink(nvlink.NVLinkMeasurement{})),
	} {
		got := s.Errored(errors.New("timed out"))
		if len(got) != 1 || health.KeyOf(got[0]) != health.SourceEntity(s.Name()) {
			t.Errorf("%s: Errored keyed %v, want %v", s.Name(), health.KeyOf(got[0]), health.SourceEntity(s.Name()))
		}
	}
}

// privateGPUSensor is a private provider appended to the stock sensors. Its
// measurement type is its own but carries GPU signals, so it feeds the
// built-in GPU checks.
type privateGPUSensor struct{ ms []health.Measurement }

func (privateGPUSensor) Name() string { return "private-gpu" }
func (s privateGPUSensor) Measure(context.Context) ([]health.Measurement, error) {
	return s.ms, nil
}
func (privateGPUSensor) Errored(error) []health.Measurement { return nil }

type privateGPU struct {
	Label  string `json:"gpu_id"`
	key    health.EntityKey
	Pstate int64 `json:"gpu_pstate"`
}

func (privateGPU) Category() health.Category           { return health.GPU }
func (privateGPU) Errors() []health.ErroredMeasurement { return nil }
func (m privateGPU) EntityID() string                  { return m.Label }

// keyedPrivateGPU opts into the explicit key; privateGPU relies on the
// EntityID compatibility key.
type keyedPrivateGPU privateGPU

func (keyedPrivateGPU) Category() health.Category           { return health.GPU }
func (keyedPrivateGPU) Errors() []health.ErroredMeasurement { return nil }
func (m keyedPrivateGPU) EntityKey() health.EntityKey       { return m.key }

func TestCollectWithPrivateGPUProvider(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	stock := toolkit.NewGPUSensor(toolkit.MockGPU(gpu.GPUMeasurement{GPUID: "GPU-A"}))
	for _, tc := range []struct {
		m         health.Measurement
		name      string
		ambiguous bool
	}{
		{name: "compatibility key, other GPU", m: privateGPU{Label: "GPU-B"}},
		{name: "compatibility key, same GPU", m: privateGPU{Label: "GPU-A"}, ambiguous: true},
		{name: "explicit key, same GPU", m: keyedPrivateGPU{Label: "private", key: health.VerifiedEntity("GPU-A")}, ambiguous: true},
		{name: "explicit key, other GPU", m: keyedPrivateGPU{Label: "GPU-A", key: health.VerifiedEntity("GPU-B")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := toolkit.Collect(cat, stock, privateGPUSensor{ms: []health.Measurement{tc.m}})
			if got := errors.Is(err, health.ErrAmbiguousMeasurement); got != tc.ambiguous {
				t.Fatalf("Collect err = %v, want ambiguous=%v", err, tc.ambiguous)
			}
		})
	}
}
