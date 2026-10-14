// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func writePCIFixture(t *testing.T, path, class string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "config"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"vendor": "0x10de\n", "device": "0x1234\n", "class": class,
		"subsystem_vendor": "0x10de\n", "subsystem_device": "0x4321\n", "revision": "0xa1\n",
		"numa_node": "0\n", "local_cpulist": "0-3,8\n",
		"max_link_speed": "64.0 GT/s PCIe\n", "max_link_width": "16\n",
		"current_link_speed": "32.0 GT/s PCIe\n", "current_link_width": "16\n",
		"aer_dev_correctable": "Receiver Error 0\nTOTAL_ERR_COR 0\n",
		"aer_dev_nonfatal":    "Completion Timeout 0\nTOTAL_ERR_NONFATAL 0\n",
		"aer_dev_fatal":       "Surprise Down Error 0\nTOTAL_ERR_FATAL 0\n",
	} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSysfsPCIeDiscovery(t *testing.T) {
	root := t.TempDir()
	bus := filepath.Join(root, "bus", "pci", "devices")
	if err := os.MkdirAll(bus, 0o755); err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(root, "devices", "pci0000:00", "0000:00:01.0")
	writePCIFixture(t, bridge, "0x060400\n")
	writePCIFixture(t, filepath.Join(bridge, "0000:65:00.0"), "0x030200\n")
	writePCIFixture(t, filepath.Join(bridge, "0000:66:00.0"), "0x020700\n")
	for _, bdf := range []string{"0000:00:01.0", "0000:65:00.0", "0000:66:00.0"} {
		target := bridge
		if bdf != "0000:00:01.0" {
			target = filepath.Join(bridge, bdf)
		}
		if err := os.Symlink(target, filepath.Join(bus, bdf)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bus, "not-a-device"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &sysfsPCIeBackend{root: bus}
	measurements, err := backend.MeasurePCIe(context.Background())
	if err != nil || len(measurements) != 10 {
		t.Fatalf("discovery: count=%d, error=%v", len(measurements), err)
	}
	for i, want := range []PCIDeviceType{PCIDeviceBridge, PCIDeviceGPU, PCIDeviceNIC} {
		m := measurements[i*3].(pcie.PCIeMeasurement)
		if len(m.Errors()) != 0 || m.Device.Type != want.String() || m.Device.VendorID != 0x10de || m.Device.DeviceID != 0x1234 {
			t.Errorf("device %d: %+v, errors=%v", i, m.Device, m.Errors())
		}
		parent := "0000:00:01.0"
		if i == 0 {
			parent = ""
		}
		if m.Device.ParentBDF != parent {
			t.Errorf("device %d parent = %q, want %q", i, m.Device.ParentBDF, parent)
		}
	}

	// An unreadable attribute must preserve the device and other usable signals.
	if err = os.WriteFile(filepath.Join(bridge, "0000:65:00.0", "class"), []byte("0x1000000"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(bridge, "0000:65:00.0", "vendor")); err != nil {
		t.Fatal(err)
	}
	measurements, err = backend.MeasurePCIe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := measurements[3].(pcie.PCIeMeasurement)
	if m.Device.BDF != "0000:65:00.0" || m.Device.DeviceID != 0x1234 || m.Device.Type != PCIDeviceUnknown.String() {
		t.Fatalf("partial device lost identity: %+v", m.Device)
	}
	var failed []string
	for _, failure := range m.Errors() {
		failed = append(failed, failure.FailedSignals()...)
	}
	for _, signal := range []string{pcie.SignalVendorID, pcie.SignalClassCode, pcie.SignalDeviceType} {
		if !slices.Contains(failed, signal) {
			t.Errorf("missing failed signal %q in %v", signal, failed)
		}
	}
}

func TestSysfsPCIeUnavailableAndCancellation(t *testing.T) {
	backend := &sysfsPCIeBackend{root: filepath.Join(t.TempDir(), "missing")}
	if _, err := backend.MeasurePCIe(context.Background()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing sysfs error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.MeasurePCIe(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled discovery error: %v", err)
	}
	backend.root = t.TempDir()
	if devices, err := backend.MeasurePCIe(context.Background()); err != nil || len(devices) != 1 || len(devices[0].(pcie.InventoryMeasurement).BDFs) != 0 {
		t.Errorf("empty inventory: %v, error=%v", devices, err)
	}
}

func TestPCIeSensor(t *testing.T) {
	sensor := NewPCIeSensor(MockPCIe(pcie.PCIeMeasurement{Device: pcie.PCIDevice{BDF: "0000:65:00.0"}}))
	measurements, err := sensor.Measure(context.Background())
	if err != nil || len(measurements) != 1 || measurements[0].Category() != health.PCIe || sensor.Name() != "pcie" {
		t.Fatalf("sensor result: %v, error=%v", measurements, err)
	}
	cause := errors.New("discovery failed")
	failed := sensor.Errored(cause)
	if len(failed) != 4 || !errors.Is(failed[0].Errors()[0].Err(), cause) {
		t.Fatalf("sensor failure: %v", failed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = sensor.Measure(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled sensor error: %v", err)
	}
}
