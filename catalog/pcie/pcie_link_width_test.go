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

func TestPCIeLinkWidth(t *testing.T) {
	budget := pciOverride("pcie_link_width", "expected_link_width_by_bdf", map[string]int64{testBDF: 16, "0000:66:00.0": 8})
	service := health.Drain(health.RemediationActionServiceRequired)
	failed := pcie.LinkMeasurement{BDF: testBDF, Width: 16}
	failed.AddError(errors.New("read failed"), pcie.SignalLinkWidth)
	unavailable := pcie.LinkMeasurement{BDF: testBDF, Width: 16}
	unavailable.AddError(health.ErrSignalUnavailable, pcie.SignalLinkWidth)
	unrelated := unavailable
	unrelated.AddError(errors.New("speed read failed"), pcie.SignalLinkSpeed)
	mixed := unrelated
	mixed.AddError(errors.New("width decode failed"), pcie.SignalLinkWidth)
	speedFailed := pcie.LinkMeasurement{BDF: testBDF, Width: 16}
	speedFailed.AddError(errors.New("speed unavailable"), pcie.SignalLinkSpeed)
	runPCIeCases(t, "pcie_link_width", map[string]pciCheckCase{
		"zero width": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, Width: 0},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"narrower than expected": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, Width: 8},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"expected width": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, Width: 16},
			wantSeverity: health.Pass,
			wantMessages: 0,
		},
		"wider than expected": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, Width: 32},
			wantAction:   service,
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"negative width": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, Width: -1},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"unconfigured device": {
			measurement:  pcie.LinkMeasurement{BDF: "0000:67:00.0", Width: 16},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"nonpositive expectation": {
			measurement:  pcie.LinkMeasurement{BDF: testBDF, Width: 16},
			overrides:    []catalog.Override{pciOverride("pcie_link_width", "expected_link_width_by_bdf", map[string]int64{testBDF: 0})},
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"collection failed": {
			measurement:  failed,
			wantAction:   health.Drain(health.RemediationActionCheckCollection),
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
		"second device expectation": {
			measurement:  pcie.LinkMeasurement{BDF: "0000:66:00.0", Width: 8},
			wantSeverity: health.Pass,
		},
		"unrelated speed failure": {
			measurement:  speedFailed,
			wantSeverity: health.Pass,
		},
		"unavailable width": {
			measurement:  unavailable,
			wantAction:   health.Drain(health.RemediationActionCheckCollection),
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"unavailable width and unrelated failure": {
			measurement:  unrelated,
			wantAction:   health.Drain(health.RemediationActionCheckCollection),
			wantSeverity: health.Unknown,
			wantMessages: 1,
		},
		"unavailable width and decode failure": {
			measurement:  mixed,
			wantAction:   health.Drain(health.RemediationActionCheckCollection),
			wantSeverity: health.Critical,
			wantMessages: 1,
		},
	}, budget)
}
