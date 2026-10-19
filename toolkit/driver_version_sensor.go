// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

// errDriverVersionUnreadable is reported when the host exposes no readable
// NVIDIA driver version.
var errDriverVersionUnreadable = errors.New("NVIDIA driver version not readable from /proc/driver/nvidia/version or NVML")

// DriverVersionSensor reports the node's NVIDIA driver version as a
// [node.DriverVersionMeasurement]. The catalog's accumulators read it to
// interpret measurements whose meaning changed between driver releases.
type DriverVersionSensor struct {
	read func() (health.DriverVersion, bool)
}

// NewDriverVersionSensor returns a sensor that reads the driver version
// from /proc/driver/nvidia/version (see [DetectDriverVersion]) and, if
// that is unreadable, from fromNVML when it is non-nil. The proc read is a
// file read with no library dependency; NVML covers a container given the
// library and device nodes but not the driver's proc tree.
func NewDriverVersionSensor(fromNVML func() (string, error)) *DriverVersionSensor {
	return &DriverVersionSensor{read: func() (health.DriverVersion, bool) {
		if v, ok := DetectDriverVersion(); ok {
			return v, true
		}
		if fromNVML == nil {
			return health.DriverVersion{}, false
		}
		raw, err := fromNVML()
		if err != nil {
			return health.DriverVersion{}, false
		}
		return health.ParseDriverVersion(raw)
	}}
}

// MockDriverVersion returns a sensor that reports version as if the host
// had, or an unreadable version when version is empty.
func MockDriverVersion(version string) *DriverVersionSensor {
	return &DriverVersionSensor{read: func() (health.DriverVersion, bool) {
		return health.ParseDriverVersion(version)
	}}
}

// Name implements [Sensor].
func (s *DriverVersionSensor) Name() string { return "driver_version" }

// Measure implements [Sensor]. An unreadable version is an errored
// measurement, not an error: it narrows what version-dependent derivations
// can do, but it is not a reason to stop collecting.
func (s *DriverVersionSensor) Measure(context.Context) ([]health.Measurement, error) {
	v, ok := s.read()
	if !ok {
		return s.Errored(errDriverVersionUnreadable), nil
	}
	return []health.Measurement{node.DriverVersionMeasurement{Version: v.Raw}}, nil
}

// Errored implements [Sensor].
func (s *DriverVersionSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{node.ErroredDriverVersion(err)}
}
