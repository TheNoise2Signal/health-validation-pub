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

func TestPCIeLinkSpeed(t *testing.T) {
	budget := pciOverride("pcie_link_speed", "min_link_speed_gt_per_sec_by_bdf", map[string]float64{testBDF: 16})
	service := health.Drain(health.RemediationActionServiceRequired)
	failed := pcie.LinkMeasurement{BDF: testBDF, SpeedGTPerSec: 16}
	failed.AddError(errors.New("read failed"), pcie.SignalLinkSpeed)
	runPCIeCases(t, "pcie_link_speed", map[string]pciCheckCase{
		"zero speed": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, SpeedGTPerSec: 0},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"below minimum": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, SpeedGTPerSec: 8},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"at minimum": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, SpeedGTPerSec: 16},
			wantSeverity: health.Pass,
			wantMessages: 0,
		},
		"above minimum": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, SpeedGTPerSec: 32},
			wantSeverity: health.Pass,
			wantMessages: 0,
		},
		"negative speed": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, SpeedGTPerSec: -1},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"unconfigured device": {
			measurement:  pcie.LinkMeasurement{BDF: "0000:67:00.0", SpeedGTPerSec: 16},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"nonpositive expectation": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, SpeedGTPerSec: 16},
			overrides:    []catalog.Override{pciOverride("pcie_link_speed", "min_link_speed_gt_per_sec_by_bdf", map[string]float64{testBDF: 0})},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"collection failed": {
			measurement:  failed,
			wantAction:   health.Drain(health.RemediationActionCheckCollection),
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
	}, budget)
}
