// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

// PCIDeviceInfo keeps inventory metadata and negotiated link signals together.
// Collection failures are recorded on Identity or Link for their affected signals.
type PCIDeviceInfo struct {
	Identity pcie.PCIeMeasurement
	Link     pcie.LinkMeasurement
}

// SysfsPCIInfo collects PCI information directly from Linux sysfs.
func SysfsPCIInfo(ctx context.Context) ([]PCIDeviceInfo, error) {
	return (&sysfsPCIeBackend{root: "/sys/bus/pci/devices"}).PCIInfo(ctx)
}

// PCIInventory groups all collected properties by device BDF.
// FieldErrors contains failures that could not be attributed to a device.
type PCIInventory struct {
	FieldErrors map[string][]string `json:"field_errors,omitempty"`
	Devices     []map[string]any    `json:"devices"`
}

// PCIInventoryFromMeasurements builds readable device records with hexadecimal
// hardware IDs, numeric measurements, and null values for failed fields.
func PCIInventoryFromMeasurements(measurements ...health.Measurement) (PCIInventory, error) {
	inventory := PCIInventory{Devices: []map[string]any{}, FieldErrors: make(map[string][]string)}

	// Backends may return either value or pointer measurements.
	measurements = slices.Clone(measurements)
	for index, measurement := range measurements {
		switch m := measurement.(type) {
		case *pcie.PCIeMeasurement:
			if m != nil {
				measurements[index] = *m
			}
		case *pcie.LinkMeasurement:
			if m != nil {
				measurements[index] = *m
			}
		case *pcie.ErrorMeasurement:
			if m != nil {
				measurements[index] = *m
			}
		case *pcie.InventoryMeasurement:
			if m != nil {
				measurements[index] = *m
			}
		}
	}
	records := make(map[string]map[string]any)
	record := func(bdf string) map[string]any {
		if existing := records[bdf]; existing != nil {
			return existing
		}
		value := map[string]any{
			"bdf": bdf, "parent_bdf": nil, "device_type": nil,
			"vendor_id": nil, "device_id": nil, "class_code": nil,
			"subsystem_vendor_id": nil, "subsystem_device_id": nil, "revision": nil,
			"driver": nil, "link": nil, "locality": nil, "sriov": nil,
			"iommu": nil, "acs": nil, "aer": nil,
		}
		records[bdf] = value
		return value
	}
	// Read identity first so separate link and counter measurements can enrich
	// it regardless of the backend's measurement order.
	for _, measurement := range measurements {
		m, ok := measurement.(pcie.PCIeMeasurement)
		if !ok || m.Device.BDF == "" {
			continue
		}
		encoded, err := json.Marshal(m.Device)
		if err != nil {
			return PCIInventory{}, fmt.Errorf("encode PCI device %s: %w", m.Device.BDF, err)
		}
		var fields map[string]any
		decoder := json.NewDecoder(strings.NewReader(string(encoded)))
		decoder.UseNumber()
		if err = decoder.Decode(&fields); err != nil {
			return PCIInventory{}, fmt.Errorf("decode PCI device %s: %w", m.Device.BDF, err)
		}
		fields["vendor_id"] = fmt.Sprintf("0x%04x", m.Device.VendorID)
		fields["device_id"] = fmt.Sprintf("0x%04x", m.Device.DeviceID)
		fields["class_code"] = fmt.Sprintf("0x%06x", m.Device.ClassCode)
		if m.Device.SubsystemVendorID != nil {
			fields["subsystem_vendor_id"] = fmt.Sprintf("0x%04x", *m.Device.SubsystemVendorID)
		}
		if m.Device.SubsystemDeviceID != nil {
			fields["subsystem_device_id"] = fmt.Sprintf("0x%04x", *m.Device.SubsystemDeviceID)
		}
		if m.Device.Revision != nil {
			fields["revision"] = fmt.Sprintf("0x%02x", *m.Device.Revision)
		}
		for name, value := range fields {
			record(m.Device.BDF)[name] = value
		}
	}
	for _, measurement := range measurements {
		switch m := measurement.(type) {
		case pcie.LinkMeasurement:
			if m.BDF != "" {
				fields := record(m.BDF)
				link, ok := fields["link"].(map[string]any)
				if !ok {
					link = make(map[string]any)
					fields["link"] = link
				}
				link["current_speed_gt_per_sec"] = m.SpeedGTPerSec
				link["current_width"] = m.Width
			}
		case pcie.ErrorMeasurement:
			if m.BDF != "" {
				record(m.BDF)["aer"] = map[string]any{
					"correctable_errors": m.CorrectableErrors,
					"nonfatal_errors":    m.NonfatalErrors,
					"fatal_errors":       m.FatalErrors,
				}
			}
		case pcie.InventoryMeasurement:
			for _, bdf := range m.BDFs {
				record(bdf)
			}
		}
	}
	for _, measurement := range measurements {
		var bdf string
		switch m := measurement.(type) {
		case pcie.PCIeMeasurement:
			bdf = m.Device.BDF
		case pcie.LinkMeasurement:
			bdf = m.BDF
		case pcie.ErrorMeasurement:
			bdf = m.BDF
		case pcie.InventoryMeasurement:
		default:
			continue
		}
		for _, failure := range measurement.Errors() {
			for _, signal := range failure.FailedSignals() {
				path := pciInventoryFieldPaths[signal]
				if path == "" {
					path = signal
				}
				fieldErrors := inventory.FieldErrors
				if bdf != "" {
					fields := record(bdf)
					fieldErrors, _ = fields["field_errors"].(map[string][]string)
					if fieldErrors == nil {
						fieldErrors = make(map[string][]string)
						fields["field_errors"] = fieldErrors
					}
					pciInventoryNullField(fields, path)
				}
				if detail := failure.Err().Error(); !slices.Contains(fieldErrors[path], detail) {
					fieldErrors[path] = append(fieldErrors[path], detail)
				}
			}
		}
	}
	bdfs := make([]string, 0, len(records))
	for bdf := range records {
		bdfs = append(bdfs, bdf)
	}
	slices.Sort(bdfs)
	for _, bdf := range bdfs {
		inventory.Devices = append(inventory.Devices, records[bdf])
	}
	return inventory, nil
}

func pciInventoryNullField(fields map[string]any, path string) {
	parent, child, nested := strings.Cut(path, ".")
	if !nested {
		fields[parent] = nil
		return
	}
	if values, ok := fields[parent].(map[string]any); ok {
		pciInventoryNullField(values, child)
	}
}

var pciInventoryFieldPaths = map[string]string{
	pcie.SignalBDF: "bdf", pcie.SignalParentBDF: "parent_bdf",
	pcie.SignalVendorID: "vendor_id", pcie.SignalDeviceID: "device_id",
	pcie.SignalClassCode: "class_code", pcie.SignalDeviceType: "device_type",
	pcie.SignalIOMMUGroup: "iommu.group_id", pcie.SignalIOMMUGroupDevices: "iommu.devices",
	pcie.SignalACSSupported: "acs.supported", pcie.SignalACSCapabilities: "acs.capabilities", pcie.SignalACSControl: "acs.control",
	pcie.SignalPCICurrentGeneration: "link.current_generation", pcie.SignalPCIMaxGeneration: "link.max_generation",
	pcie.SignalPCIMaxLinkSpeed: "link.max_speed_gt_per_sec", pcie.SignalPCIMaxLinkWidth: "link.max_width",
	pcie.SignalPCINUMANode: "locality.numa_node", pcie.SignalPCILocalCPUs: "locality.local_cpus",
	pcie.SignalPCIDriver: "driver", pcie.SignalPCISubsystemVendorID: "subsystem_vendor_id",
	pcie.SignalPCISubsystemDeviceID: "subsystem_device_id", pcie.SignalPCIRevision: "revision",
	pcie.SignalPCIPhysicalFunction: "sriov.physical_function_bdf", pcie.SignalPCIVirtualFunctions: "sriov.virtual_function_bdfs",
	pcie.SignalPCITotalVFs: "sriov.total_vfs", pcie.SignalPCIEnabledVFs: "sriov.enabled_vfs",
	pcie.SignalLinkBDF: "bdf", pcie.SignalLinkSpeed: "link.current_speed_gt_per_sec", pcie.SignalLinkWidth: "link.current_width",
	pcie.SignalErrorBDF: "bdf", pcie.SignalCorrectableErrors: "aer.correctable_errors",
	pcie.SignalNonfatalErrors: "aer.nonfatal_errors", pcie.SignalFatalErrors: "aer.fatal_errors",
	pcie.SignalDeviceBDFs: "device_bdfs",
}
