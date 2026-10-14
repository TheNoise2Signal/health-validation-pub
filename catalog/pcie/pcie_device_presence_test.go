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

func TestPCIeDevicePresence(t *testing.T) {
	budget := pciOverride("pcie_device_presence", "expected_device_bdfs", []string{testBDF, "0001:65:00.0"})
	service := health.Drain(health.RemediationActionServiceRequired)
	runPCIeCases(t, "pcie_device_presence", map[string]pciCheckCase{
		"all expected devices": {
			measurement:  pcie.InventoryMeasurement{BDFs: []string{testBDF, "0001:65:00.0"}},
			wantSeverity: health.Pass,
		},
		"extra devices allowed": {
			measurement:  pcie.InventoryMeasurement{BDFs: []string{testBDF, "0001:65:00.0", "0002:00:00.0"}},
			wantSeverity: health.Pass,
		},
		"missing expected device": {
			measurement:  pcie.InventoryMeasurement{BDFs: []string{testBDF}},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"empty inventory": {
			measurement:  pcie.InventoryMeasurement{},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"nil expectations": {
			measurement:  pcie.InventoryMeasurement{},
			overrides:    []catalog.Override{pciOverride("pcie_device_presence", "expected_device_bdfs", []string(nil))},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"empty expectations": {
			measurement:  pcie.InventoryMeasurement{},
			overrides:    []catalog.Override{pciOverride("pcie_device_presence", "expected_device_bdfs", []string{})},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"empty expected BDF": {
			measurement:  pcie.InventoryMeasurement{},
			overrides:    []catalog.Override{pciOverride("pcie_device_presence", "expected_device_bdfs", []string{""})},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"discovery failed": {
			measurement:  pcie.ErroredInventory(errors.New("discovery failed")),
			wantAction:   health.Drain(health.RemediationActionCheckCollection),
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
	}, budget)
}
