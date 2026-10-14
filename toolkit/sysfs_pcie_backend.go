// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

var pciBDFPattern = regexp.MustCompile(`^[[:xdigit:]]{4,8}:[[:xdigit:]]{2}:[01][[:xdigit:]]\.[0-7]$`)

type sysfsPCIeBackend struct {
	root string
}

// SysfsPCIe discovers PCI devices from Linux sysfs without external commands.
func SysfsPCIe() PCIeBackend {
	return &sysfsPCIeBackend{root: "/sys/bus/pci/devices"}
}

func (b *sysfsPCIeBackend) MeasurePCIe(ctx context.Context) ([]health.Measurement, error) {
	devices, err := b.PCIInfo(ctx)
	if err != nil {
		return nil, err
	}
	measurements := make([]health.Measurement, 0, 3*len(devices)+1)
	inventory := pcie.InventoryMeasurement{BDFs: make([]string, 0, len(devices))}
	for _, device := range devices {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		bdf := device.Identity.Device.BDF
		measurements = append(measurements, device.Identity, device.Link)
		if counters, applicable := collectPCIErrorCounters(ctx, filepath.Join(b.root, bdf), bdf); applicable {
			measurements = append(measurements, counters)
		}
		inventory.BDFs = append(inventory.BDFs, bdf)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return append(measurements, inventory), nil
}

func (b *sysfsPCIeBackend) PCIInfo(ctx context.Context) ([]PCIDeviceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(b.root)
	if err != nil {
		return nil, fmt.Errorf("discover PCI devices in %s: %w", b.root, err)
	}
	devices := make([]PCIDeviceInfo, 0, len(entries))
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !pciBDFPattern.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(b.root, entry.Name())
		m := pcie.PCIeMeasurement{Device: pcie.PCIDevice{BDF: strings.ToLower(entry.Name()), Type: PCIDeviceUnknown.String()}}
		var resolved string
		resolved, err = filepath.EvalSymlinks(path)
		if err != nil {
			m.AddError(fmt.Errorf("resolve PCI device %s: %w", path, err), pcie.SignalParentBDF)
		} else if parent := filepath.Base(filepath.Dir(resolved)); pciBDFPattern.MatchString(parent) {
			m.Device.ParentBDF = strings.ToLower(parent)
		}
		for _, field := range []struct {
			name   string
			signal string
			bits   int
		}{
			{name: "vendor", signal: pcie.SignalVendorID, bits: 16},
			{name: "device", signal: pcie.SignalDeviceID, bits: 16},
			{name: "class", signal: pcie.SignalClassCode, bits: 24},
		} {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			var value uint64
			value, err = readPCIHex(filepath.Join(path, field.name), field.bits)
			if err != nil {
				if field.signal == pcie.SignalClassCode {
					m.AddError(err, field.signal, pcie.SignalDeviceType)
				} else {
					m.AddError(err, field.signal)
				}
				continue
			}
			switch field.signal {
			case pcie.SignalVendorID:
				m.Device.VendorID = uint16(value)
			case pcie.SignalDeviceID:
				m.Device.DeviceID = uint16(value)
			case pcie.SignalClassCode:
				m.Device.ClassCode = uint32(value)
				m.Device.Type = pciDeviceType(uint32(value)).String()
			}
		}
		devices = append(devices, collectPCIDeviceInfo(ctx, path, m))
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}

func readPCIHex(path string, bits int) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read PCI attribute %s: %w", path, err)
	}
	value, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(string(data)), "0x"), 16, bits)
	if err != nil {
		return 0, fmt.Errorf("parse PCI attribute %s: %w", path, err)
	}
	return value, nil
}

func pciDeviceType(classCode uint32) PCIDeviceType {
	switch classCode >> 16 {
	case 0x03: // Display controllers.
		return PCIDeviceGPU
	case 0x02: // Network controllers.
		return PCIDeviceNIC
	case 0x06: // Bridges, including switch ports; class alone cannot identify a switch.
		return PCIDeviceBridge
	default:
		return PCIDeviceUnknown
	}
}
