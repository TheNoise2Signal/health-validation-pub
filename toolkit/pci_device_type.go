// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"encoding/json"
	"fmt"
)

// PCIDeviceType is a coarse classification; the raw PCI class code is retained separately.
type PCIDeviceType int

const (
	PCIDeviceUnknown PCIDeviceType = iota
	PCIDeviceGPU
	PCIDeviceNIC
	PCIDeviceBridge
	// PCIDeviceSwitch requires topology-aware classification, not just a bridge class code.
	PCIDeviceSwitch
)

var pciDeviceTypeNames = map[PCIDeviceType]string{
	PCIDeviceUnknown: "unknown", PCIDeviceGPU: "gpu", PCIDeviceNIC: "nic",
	PCIDeviceBridge: "bridge", PCIDeviceSwitch: "switch",
}

var pciDeviceTypeFromName = map[string]PCIDeviceType{
	"unknown": PCIDeviceUnknown, "gpu": PCIDeviceGPU, "nic": PCIDeviceNIC,
	"bridge": PCIDeviceBridge, "switch": PCIDeviceSwitch,
}

func (t PCIDeviceType) String() string {
	if name, ok := pciDeviceTypeNames[t]; ok {
		return name
	}
	return fmt.Sprintf("PCIDeviceType(%d)", int(t))
}

func (t PCIDeviceType) MarshalJSON() ([]byte, error) {
	name, ok := pciDeviceTypeNames[t]
	if !ok {
		return nil, fmt.Errorf("unknown PCI device type value %d", int(t))
	}
	return json.Marshal(name)
}

func (t *PCIDeviceType) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	value, ok := pciDeviceTypeFromName[name]
	if !ok {
		return fmt.Errorf("unknown PCI device type %q", name)
	}
	*t = value
	return nil
}
