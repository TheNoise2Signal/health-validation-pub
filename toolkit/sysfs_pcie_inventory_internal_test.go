// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

type pciInventoryDeviceFixture struct {
	bdf       string
	parentBDF string
	wantType  PCIDeviceType
	classCode uint32
	vendorID  uint16
	deviceID  uint16
}

// Derived from a verbose PCI listing: domains and hardware IDs are synthetic.
// Parent relationships follow each bridge's secondary bus within its domain.
var pciInventoryDevices = []pciInventoryDeviceFixture{
	{bdf: "0000:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2001},
	{bdf: "0000:01:00.0", parentBDF: "0000:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0000:02:00.0", parentBDF: "0000:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0000:02:03.0", parentBDF: "0000:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0000:03:00.0", parentBDF: "0000:02:00.0", wantType: PCIDeviceNIC, classCode: 0x020700, vendorID: 0x1003, deviceID: 0x2007},
	{bdf: "0000:04:00.0", parentBDF: "0000:02:03.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0000:05:08.0", parentBDF: "0000:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0000:05:0c.0", parentBDF: "0000:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0000:07:00.0", parentBDF: "0000:05:0c.0", wantType: PCIDeviceUnknown, classCode: 0x010802, vendorID: 0x1002, deviceID: 0x2006},
	{bdf: "0001:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2001},
	{bdf: "0001:01:00.0", parentBDF: "0001:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0001:02:00.0", parentBDF: "0001:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0001:02:01.0", parentBDF: "0001:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0001:03:00.0", parentBDF: "0001:02:00.0", wantType: PCIDeviceNIC, classCode: 0x020700, vendorID: 0x1003, deviceID: 0x2007},
	{bdf: "0001:04:00.0", parentBDF: "0001:02:01.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0001:05:08.0", parentBDF: "0001:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0001:05:0c.0", parentBDF: "0001:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0001:07:00.0", parentBDF: "0001:05:0c.0", wantType: PCIDeviceUnknown, classCode: 0x010802, vendorID: 0x1002, deviceID: 0x2006},
	{bdf: "0002:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2002},
	{bdf: "0002:01:00.0", parentBDF: "0002:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1001, deviceID: 0x2004},
	{bdf: "0002:02:01.0", parentBDF: "0002:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1001, deviceID: 0x2004},
	{bdf: "0002:02:02.0", parentBDF: "0002:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1001, deviceID: 0x2004},
	{bdf: "0002:02:03.0", parentBDF: "0002:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1001, deviceID: 0x2004},
	{bdf: "0002:02:04.0", parentBDF: "0002:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1001, deviceID: 0x2004},
	{bdf: "0002:02:05.0", parentBDF: "0002:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1001, deviceID: 0x2004},
	{bdf: "0002:02:06.0", parentBDF: "0002:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1001, deviceID: 0x2004},
	{bdf: "0002:02:07.0", parentBDF: "0002:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1001, deviceID: 0x2004},
	{bdf: "0002:06:00.0", parentBDF: "0002:02:04.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1005, deviceID: 0x200e},
	{bdf: "0002:07:00.0", parentBDF: "0002:06:00.0", wantType: PCIDeviceGPU, classCode: 0x030000, vendorID: 0x1005, deviceID: 0x200f},
	{bdf: "0002:08:00.0", parentBDF: "0002:02:05.0", wantType: PCIDeviceUnknown, classCode: 0x0c0330, vendorID: 0x1004, deviceID: 0x200d},
	{bdf: "0002:09:00.0", parentBDF: "0002:02:06.0", wantType: PCIDeviceNIC, classCode: 0x020000, vendorID: 0x1006, deviceID: 0x2010},
	{bdf: "0003:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2001},
	{bdf: "0004:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2000},
	{bdf: "0004:01:00.0", parentBDF: "0004:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0004:02:00.0", parentBDF: "0004:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0004:02:03.0", parentBDF: "0004:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0004:03:00.0", parentBDF: "0004:02:00.0", wantType: PCIDeviceUnknown, classCode: 0x080100, vendorID: 0x1003, deviceID: 0x200a},
	{bdf: "0004:04:00.0", parentBDF: "0004:02:03.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0004:05:00.0", parentBDF: "0004:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0004:06:00.0", parentBDF: "0004:05:00.0", wantType: PCIDeviceGPU, classCode: 0x030200, vendorID: 0x1000, deviceID: 0x2003},
	{bdf: "0005:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2000},
	{bdf: "0005:01:00.0", parentBDF: "0005:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0005:02:00.0", parentBDF: "0005:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0005:02:01.0", parentBDF: "0005:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0005:03:00.0", parentBDF: "0005:02:00.0", wantType: PCIDeviceUnknown, classCode: 0x080100, vendorID: 0x1003, deviceID: 0x200a},
	{bdf: "0005:04:00.0", parentBDF: "0005:02:01.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0005:05:00.0", parentBDF: "0005:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0005:06:00.0", parentBDF: "0005:05:00.0", wantType: PCIDeviceGPU, classCode: 0x030200, vendorID: 0x1000, deviceID: 0x2003},
	{bdf: "0006:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2001},
	{bdf: "0006:01:00.0", parentBDF: "0006:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0006:02:00.0", parentBDF: "0006:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0006:02:03.0", parentBDF: "0006:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0006:03:00.0", parentBDF: "0006:02:00.0", wantType: PCIDeviceNIC, classCode: 0x020700, vendorID: 0x1003, deviceID: 0x2007},
	{bdf: "0006:04:00.0", parentBDF: "0006:02:03.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0006:05:08.0", parentBDF: "0006:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0006:05:0c.0", parentBDF: "0006:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0006:07:00.0", parentBDF: "0006:05:0c.0", wantType: PCIDeviceUnknown, classCode: 0x010802, vendorID: 0x1002, deviceID: 0x2006},
	{bdf: "0007:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2001},
	{bdf: "0007:01:00.0", parentBDF: "0007:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0007:02:00.0", parentBDF: "0007:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0007:02:01.0", parentBDF: "0007:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0007:03:00.0", parentBDF: "0007:02:00.0", wantType: PCIDeviceNIC, classCode: 0x020700, vendorID: 0x1003, deviceID: 0x2007},
	{bdf: "0007:04:00.0", parentBDF: "0007:02:01.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0007:05:08.0", parentBDF: "0007:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0007:05:0c.0", parentBDF: "0007:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "0007:07:00.0", parentBDF: "0007:05:0c.0", wantType: PCIDeviceUnknown, classCode: 0x010802, vendorID: 0x1002, deviceID: 0x2006},
	{bdf: "0008:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2002},
	{bdf: "0008:01:00.0", parentBDF: "0008:00:00.0", wantType: PCIDeviceUnknown, classCode: 0x010802, vendorID: 0x1002, deviceID: 0x2005},
	{bdf: "0009:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2001},
	{bdf: "0009:01:00.0", parentBDF: "0009:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2008},
	{bdf: "0009:02:00.0", parentBDF: "0009:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2008},
	{bdf: "0009:02:02.0", parentBDF: "0009:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2008},
	{bdf: "0009:03:00.0", parentBDF: "0009:02:00.0", wantType: PCIDeviceNIC, classCode: 0x020700, vendorID: 0x1003, deviceID: 0x200b},
	{bdf: "0009:03:00.1", parentBDF: "0009:02:00.0", wantType: PCIDeviceNIC, classCode: 0x020000, vendorID: 0x1003, deviceID: 0x200b},
	{bdf: "0009:03:00.2", parentBDF: "0009:02:00.0", wantType: PCIDeviceUnknown, classCode: 0x080100, vendorID: 0x1003, deviceID: 0x200c},
	{bdf: "0009:04:00.0", parentBDF: "0009:02:02.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2008},
	{bdf: "0009:05:00.0", parentBDF: "0009:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2008},
	{bdf: "0009:05:04.0", parentBDF: "0009:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2008},
	{bdf: "0009:05:08.0", parentBDF: "0009:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2008},
	{bdf: "0009:05:0c.0", parentBDF: "0009:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2008},
	{bdf: "000a:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2000},
	{bdf: "000a:01:00.0", parentBDF: "000a:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000a:02:00.0", parentBDF: "000a:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000a:02:03.0", parentBDF: "000a:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000a:03:00.0", parentBDF: "000a:02:00.0", wantType: PCIDeviceUnknown, classCode: 0x080100, vendorID: 0x1003, deviceID: 0x200a},
	{bdf: "000a:04:00.0", parentBDF: "000a:02:03.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000a:05:00.0", parentBDF: "000a:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000a:06:00.0", parentBDF: "000a:05:00.0", wantType: PCIDeviceGPU, classCode: 0x030200, vendorID: 0x1000, deviceID: 0x2003},
	{bdf: "000b:00:00.0", parentBDF: "", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1000, deviceID: 0x2000},
	{bdf: "000b:01:00.0", parentBDF: "000b:00:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000b:02:00.0", parentBDF: "000b:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000b:02:01.0", parentBDF: "000b:01:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000b:03:00.0", parentBDF: "000b:02:00.0", wantType: PCIDeviceUnknown, classCode: 0x080100, vendorID: 0x1003, deviceID: 0x200a},
	{bdf: "000b:04:00.0", parentBDF: "000b:02:01.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000b:05:00.0", parentBDF: "000b:04:00.0", wantType: PCIDeviceBridge, classCode: 0x060400, vendorID: 0x1003, deviceID: 0x2009},
	{bdf: "000b:06:00.0", parentBDF: "000b:05:00.0", wantType: PCIDeviceGPU, classCode: 0x030200, vendorID: 0x1000, deviceID: 0x2003},
}

func TestSysfsPCIeInventorySnapshot(t *testing.T) {
	root := t.TempDir()
	bus := filepath.Join(root, "bus", "pci", "devices")
	if err := os.MkdirAll(bus, 0o755); err != nil {
		t.Fatal(err)
	}
	byBDF := make(map[string]pciInventoryDeviceFixture, len(pciInventoryDevices))
	for _, device := range pciInventoryDevices {
		if _, exists := byBDF[device.bdf]; exists {
			t.Fatalf("duplicate fixture BDF %s", device.bdf)
		}
		byBDF[device.bdf] = device
	}
	var devicePath func(string, map[string]bool) string
	devicePath = func(bdf string, visiting map[string]bool) string {
		if visiting[bdf] {
			t.Fatalf("cycle in fixture topology at %s", bdf)
		}
		device, exists := byBDF[bdf]
		if !exists {
			t.Fatalf("missing fixture parent %s", bdf)
		}
		visiting[bdf] = true
		defer delete(visiting, bdf)
		if device.parentBDF == "" {
			domain, _, _ := strings.Cut(bdf, ":")
			return filepath.Join(root, "devices", "pci"+domain+":00", bdf)
		}
		return filepath.Join(devicePath(device.parentBDF, visiting), bdf)
	}
	for _, device := range pciInventoryDevices {
		path := devicePath(device.bdf, make(map[string]bool))
		writePCIFixture(t, path, fmt.Sprintf("0x%06x\n", device.classCode))
		for name, value := range map[string]uint16{"vendor": device.vendorID, "device": device.deviceID} {
			if err := os.WriteFile(filepath.Join(path, name), []byte(fmt.Sprintf("0x%04x\n", value)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(path, filepath.Join(bus, device.bdf)); err != nil {
			t.Fatal(err)
		}
	}
	sensor := NewPCIeSensor(&sysfsPCIeBackend{root: bus})
	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 3*len(pciInventoryDevices)+1 {
		t.Fatalf("discovered %d devices, want %d", len(measurements), len(pciInventoryDevices))
	}
	classCounts := make(map[uint32]int)
	typeCounts := make(map[string]int)
	seen := make(map[string]bool)
	for _, measurement := range measurements {
		identity, ok := measurement.(pcie.PCIeMeasurement)
		if !ok {
			continue
		}
		signals := identity.DerivedSignals()
		bdf := signals["bdf"].(string)
		want, exists := byBDF[bdf]
		if !exists || seen[bdf] {
			t.Fatalf("unexpected or duplicate discovered BDF %s", bdf)
		}
		seen[bdf] = true
		if len(measurement.Errors()) != 0 {
			t.Errorf("device %s collection errors: %v", bdf, measurement.Errors())
		}
		for name, expected := range map[string]any{
			"parent_bdf": want.parentBDF, "vendor_id": int64(want.vendorID),
			"device_id": int64(want.deviceID), "class_code": int64(want.classCode),
			"device_type": want.wantType.String(),
		} {
			if signals[name] != expected {
				t.Errorf("device %s signal %s = %v, want %v", bdf, name, signals[name], expected)
			}
		}
		classCounts[want.classCode]++
		typeCounts[signals["device_type"].(string)]++
	}
	for class, count := range map[uint32]int{
		0x060400: 73, 0x020700: 5, 0x010802: 5, 0x030000: 1,
		0x0c0330: 1, 0x020000: 2, 0x080100: 5, 0x030200: 4,
	} {
		if classCounts[class] != count {
			t.Errorf("class 0x%06x: count=%d, want %d", class, classCounts[class], count)
		}
	}
	for deviceType, count := range map[string]int{"bridge": 73, "nic": 7, "gpu": 5, "unknown": 11} {
		if typeCounts[deviceType] != count {
			t.Errorf("type %s: count=%d, want %d", deviceType, typeCounts[deviceType], count)
		}
	}
}
