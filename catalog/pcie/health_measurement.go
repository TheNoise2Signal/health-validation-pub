// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie

import "github.com/dsx-ai-factory/health-validation/catalog/health"

const (
	SignalLinkBDF           = "pcie_link_bdf"
	SignalLinkSpeed         = "pcie_link_speed_gt_per_sec"
	SignalLinkWidth         = "pcie_link_width"
	SignalErrorBDF          = "pcie_error_bdf"
	SignalCorrectableErrors = "pcie_correctable_errors"
	SignalNonfatalErrors    = "pcie_nonfatal_errors"
	SignalFatalErrors       = "pcie_fatal_errors"
	SignalDeviceBDFs        = "pci_device_bdfs"
)

var (
	LinkMeasurementSignals      = []string{SignalLinkBDF, SignalLinkSpeed, SignalLinkWidth}
	ErrorMeasurementSignals     = []string{SignalErrorBDF, SignalCorrectableErrors, SignalNonfatalErrors, SignalFatalErrors}
	InventoryMeasurementSignals = []string{SignalDeviceBDFs}
)

// LinkMeasurement describes a negotiated PCIe link; speed is in GT/s.
type LinkMeasurement struct {
	BDF           string `json:"pcie_link_bdf"`
	errors        []health.ErroredMeasurement
	SpeedGTPerSec float64 `json:"pcie_link_speed_gt_per_sec"`
	Width         int64   `json:"pcie_link_width"`
}

func (LinkMeasurement) Category() health.Category             { return health.PCIe }
func (m LinkMeasurement) EntityID() string                    { return m.BDF }
func (m LinkMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
func (m *LinkMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

func ErroredLink(err error) LinkMeasurement {
	var m LinkMeasurement
	m.AddError(err, LinkMeasurementSignals...)
	return m
}

// ErrorMeasurement carries cumulative AER counts since their last reset, not rates.
type ErrorMeasurement struct {
	BDF               string `json:"pcie_error_bdf"`
	errors            []health.ErroredMeasurement
	CorrectableErrors int64 `json:"pcie_correctable_errors"`
	NonfatalErrors    int64 `json:"pcie_nonfatal_errors"`
	FatalErrors       int64 `json:"pcie_fatal_errors"`
}

func (ErrorMeasurement) Category() health.Category             { return health.PCIe }
func (m ErrorMeasurement) EntityID() string                    { return m.BDF }
func (m ErrorMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
func (m *ErrorMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

func ErroredErrorCounters(err error) ErrorMeasurement {
	var m ErrorMeasurement
	m.AddError(err, ErrorMeasurementSignals...)
	return m
}

// InventoryMeasurement includes the full observed BDF list, even when it is empty.
type InventoryMeasurement struct {
	errors []health.ErroredMeasurement
	BDFs   []string `json:"pci_device_bdfs"`
}

func (InventoryMeasurement) Category() health.Category             { return health.PCIe }
func (m InventoryMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
func (m *InventoryMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

func ErroredInventory(err error) InventoryMeasurement {
	var m InventoryMeasurement
	m.AddError(err, InventoryMeasurementSignals...)
	return m
}
