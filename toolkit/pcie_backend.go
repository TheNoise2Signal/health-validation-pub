// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// PCIeBackend collects PCI identity, links, errors, and the node inventory.
type PCIeBackend interface {
	MeasurePCIe(context.Context) ([]health.Measurement, error)
}

// MockPCIe returns a backend supplying the given PCIe measurements.
func MockPCIe(measurements ...health.Measurement) PCIeBackend {
	return mockPCIeBackend{measurements: slices.Clone(measurements)}
}

type mockPCIeBackend struct {
	measurements []health.Measurement
}

func (m mockPCIeBackend) MeasurePCIe(ctx context.Context) ([]health.Measurement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return slices.Clone(m.measurements), nil
}
