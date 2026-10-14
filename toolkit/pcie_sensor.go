// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

// PCIeSensor collects device identity, link, error, and inventory measurements.
type PCIeSensor struct {
	backend PCIeBackend
}

// NewPCIeSensor creates a sensor backed by PCI device discovery.
func NewPCIeSensor(backend PCIeBackend) *PCIeSensor {
	return &PCIeSensor{backend: backend}
}

func (s *PCIeSensor) Name() string { return "pcie" }

func (s *PCIeSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	measurements, err := s.backend.MeasurePCIe(ctx)
	if err != nil {
		return nil, err
	}
	return measurements, nil
}

func (s *PCIeSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{
		pcie.ErroredPCIe(err), pcie.ErroredLink(err),
		pcie.ErroredErrorCounters(err), pcie.ErroredInventory(err),
	}
}

var _ Sensor = (*PCIeSensor)(nil)
