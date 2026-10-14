// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pcie defines PCI device identity and PCIe measurements.
package pcie

import (
	"encoding/json"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

const (
	SignalBDF                  = "bdf"
	SignalParentBDF            = "parent_bdf"
	SignalVendorID             = "vendor_id"
	SignalDeviceID             = "device_id"
	SignalClassCode            = "class_code"
	SignalDeviceType           = "device_type"
	SignalIOMMUGroup           = "iommu_group"
	SignalIOMMUGroupDevices    = "iommu_group_devices"
	SignalACSSupported         = "acs_supported"
	SignalACSCapabilities      = "acs_capabilities"
	SignalACSControl           = "acs_control"
	SignalPCICurrentGeneration = "pci_current_generation"
	SignalPCIMaxGeneration     = "pci_max_generation"
	SignalPCIMaxLinkSpeed      = "pci_max_link_speed_gt_per_sec"
	SignalPCIMaxLinkWidth      = "pci_max_link_width"
	SignalPCINUMANode          = "pci_numa_node"
	SignalPCILocalCPUs         = "pci_local_cpus"
	SignalPCIDriver            = "pci_driver"
	SignalPCISubsystemVendorID = "pci_subsystem_vendor_id"
	SignalPCISubsystemDeviceID = "pci_subsystem_device_id"
	SignalPCIRevision          = "pci_revision"
	SignalPCIPhysicalFunction  = "pci_physical_function_bdf"
	SignalPCIVirtualFunctions  = "pci_virtual_function_bdfs"
	SignalPCITotalVFs          = "pci_total_vfs"
	SignalPCIEnabledVFs        = "pci_enabled_vfs"
)

var MeasurementSignals = []string{
	SignalBDF, SignalParentBDF, SignalVendorID, SignalDeviceID, SignalClassCode, SignalDeviceType,
	SignalIOMMUGroup, SignalIOMMUGroupDevices, SignalACSSupported, SignalACSCapabilities, SignalACSControl,
	SignalPCICurrentGeneration, SignalPCIMaxGeneration, SignalPCIMaxLinkSpeed, SignalPCIMaxLinkWidth, SignalPCINUMANode, SignalPCILocalCPUs, SignalPCIDriver, SignalPCISubsystemVendorID, SignalPCISubsystemDeviceID, SignalPCIRevision, SignalPCIPhysicalFunction, SignalPCIVirtualFunctions, SignalPCITotalVFs, SignalPCIEnabledVFs,
}

// PCIDevice identifies one PCI function. BDF includes the domain.
type PCIDevice struct {
	Link              *PCILinkInfo     `json:"link"`
	Locality          *PCILocalityInfo `json:"locality"`
	SRIOV             *PCISRIOVInfo    `json:"sriov"`
	Driver            *string          `json:"driver"`
	SubsystemVendorID *uint16          `json:"subsystem_vendor_id"`
	SubsystemDeviceID *uint16          `json:"subsystem_device_id"`
	Revision          *uint8           `json:"revision"`
	IOMMU             *IOMMUInfo       `json:"iommu"`
	ACS               *ACSInfo         `json:"acs"`
	BDF               string           `json:"bdf"`
	ParentBDF         string           `json:"parent_bdf"`
	Type              string           `json:"device_type"`
	ClassCode         uint32           `json:"class_code"`
	VendorID          uint16           `json:"vendor_id"`
	DeviceID          uint16           `json:"device_id"`
}

// PCILinkInfo records negotiated settings and the device's link capabilities.
// Nil fields are unavailable; maximum capability is not a health expectation.
type PCILinkInfo struct {
	CurrentSpeedGTPerSec *float64 `json:"current_speed_gt_per_sec"`
	MaxSpeedGTPerSec     *float64 `json:"max_speed_gt_per_sec"`
	CurrentWidth         *int64   `json:"current_width"`
	MaxWidth             *int64   `json:"max_width"`
	CurrentGeneration    *int64   `json:"current_generation"`
	MaxGeneration        *int64   `json:"max_generation"`
}

// PCILocalityInfo records the kernel NUMA assignment and nearby CPU list.
// NUMANode=-1 means the kernel has no NUMA assignment.
type PCILocalityInfo struct {
	LocalCPUs *string `json:"local_cpus"`
	NUMANode  *int64  `json:"numa_node"`
}

// PCISRIOVInfo records a VF's physical function or a PF's virtual functions.
type PCISRIOVInfo struct {
	PhysicalFunctionBDF *string  `json:"physical_function_bdf"`
	TotalVFs            *int64   `json:"total_vfs"`
	EnabledVFs          *int64   `json:"enabled_vfs"`
	VirtualFunctionBDFs []string `json:"virtual_function_bdfs"`
}

// IOMMUInfo records group membership. GroupID is -1 when no group is assigned.
type IOMMUInfo struct {
	Devices []string `json:"devices"`
	GroupID int64    `json:"group_id"`
}

// ACSInfo preserves the PCI ACS capability and control registers.
// A nil ACSInfo means unavailable; Supported=false means the capability is absent.
type ACSInfo struct {
	Capabilities uint16 `json:"capabilities"`
	Control      uint16 `json:"control"`
	Supported    bool   `json:"supported"`
}

// PCIeMeasurement carries one device's inventory and collection failures.
type PCIeMeasurement struct {
	errors []health.ErroredMeasurement
	Device PCIDevice `json:"-"`
}

func (PCIeMeasurement) Category() health.Category { return health.PCIe }

func (m PCIeMeasurement) Errors() []health.ErroredMeasurement { return m.errors }

func (m PCIeMeasurement) MarshalJSON() ([]byte, error) { return json.Marshal(m.Device) }

func (m *PCIeMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// ErroredPCIe marks all inventory signals unavailable when discovery fails.
func ErroredPCIe(err error) PCIeMeasurement {
	var m PCIeMeasurement
	m.AddError(err, MeasurementSignals...)
	return m
}

// DerivedSignals flattens device identity into the CEL signal schema.
func (m PCIeMeasurement) DerivedSignals() map[string]any {
	group := int64(-1)
	devices := []string{}
	if m.Device.IOMMU != nil {
		group = m.Device.IOMMU.GroupID
		devices = m.Device.IOMMU.Devices
	}
	acs := ACSInfo{}
	if m.Device.ACS != nil {
		acs = *m.Device.ACS
	}
	signals := map[string]any{
		SignalBDF: m.Device.BDF, SignalParentBDF: m.Device.ParentBDF,
		SignalVendorID: int64(m.Device.VendorID), SignalDeviceID: int64(m.Device.DeviceID),
		SignalClassCode: int64(m.Device.ClassCode), SignalDeviceType: m.Device.Type,
		SignalIOMMUGroup: group, SignalIOMMUGroupDevices: devices,
		SignalACSSupported: acs.Supported, SignalACSCapabilities: int64(acs.Capabilities), SignalACSControl: int64(acs.Control),

		SignalPCICurrentGeneration: int64(-1), SignalPCIMaxGeneration: int64(-1),
		SignalPCIMaxLinkSpeed: float64(-1), SignalPCIMaxLinkWidth: int64(-1),
		SignalPCINUMANode: int64(-1), SignalPCILocalCPUs: "", SignalPCIDriver: "",
		SignalPCISubsystemVendorID: int64(-1), SignalPCISubsystemDeviceID: int64(-1), SignalPCIRevision: int64(-1),
		SignalPCIPhysicalFunction: "", SignalPCIVirtualFunctions: []string{}, SignalPCITotalVFs: int64(-1), SignalPCIEnabledVFs: int64(-1),
	}
	if link := m.Device.Link; link != nil {
		if link.CurrentGeneration != nil {
			signals[SignalPCICurrentGeneration] = *link.CurrentGeneration
		}
		if link.MaxGeneration != nil {
			signals[SignalPCIMaxGeneration] = *link.MaxGeneration
		}
		if link.MaxSpeedGTPerSec != nil {
			signals[SignalPCIMaxLinkSpeed] = *link.MaxSpeedGTPerSec
		}
		if link.MaxWidth != nil {
			signals[SignalPCIMaxLinkWidth] = *link.MaxWidth
		}
	}
	if locality := m.Device.Locality; locality != nil {
		if locality.NUMANode != nil {
			signals[SignalPCINUMANode] = *locality.NUMANode
		}
		if locality.LocalCPUs != nil {
			signals[SignalPCILocalCPUs] = *locality.LocalCPUs
		}
	}
	if m.Device.Driver != nil {
		signals[SignalPCIDriver] = *m.Device.Driver
	}
	if m.Device.SubsystemVendorID != nil {
		signals[SignalPCISubsystemVendorID] = int64(*m.Device.SubsystemVendorID)
	}
	if m.Device.SubsystemDeviceID != nil {
		signals[SignalPCISubsystemDeviceID] = int64(*m.Device.SubsystemDeviceID)
	}
	if m.Device.Revision != nil {
		signals[SignalPCIRevision] = int64(*m.Device.Revision)
	}
	if sriov := m.Device.SRIOV; sriov != nil {
		signals[SignalPCIVirtualFunctions] = sriov.VirtualFunctionBDFs
		if sriov.PhysicalFunctionBDF != nil {
			signals[SignalPCIPhysicalFunction] = *sriov.PhysicalFunctionBDF
		}
		if sriov.TotalVFs != nil {
			signals[SignalPCITotalVFs] = *sriov.TotalVFs
		}
		if sriov.EnabledVFs != nil {
			signals[SignalPCIEnabledVFs] = *sriov.EnabledVFs
		}
	}
	return signals
}
