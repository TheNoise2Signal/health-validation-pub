// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func collectPCIDeviceInfo(ctx context.Context, path string, m pcie.PCIeMeasurement) PCIDeviceInfo {
	collectPCIIsolation(ctx, path, &m)
	collectPCIProperties(ctx, path, &m)
	link := collectPCILink(ctx, path, m.Device.BDF)
	m.Device.Link = &pcie.PCILinkInfo{}
	if !pciSignalFailed(link.Errors(), pcie.SignalLinkSpeed) {
		m.Device.Link.CurrentSpeedGTPerSec = &link.SpeedGTPerSec
		m.Device.Link.CurrentGeneration = pciGeneration(link.SpeedGTPerSec)
		if m.Device.Link.CurrentGeneration == nil {
			m.AddError(fmt.Errorf("%w: unrecognized PCIe speed %g GT/s", health.ErrSignalUnavailable, link.SpeedGTPerSec), pcie.SignalPCICurrentGeneration)
		}
	} else {
		for _, failure := range link.Errors() {
			if slices.Contains(failure.FailedSignals(), pcie.SignalLinkSpeed) {
				m.AddError(failure.Err(), pcie.SignalPCICurrentGeneration)
			}
		}
	}
	if !pciSignalFailed(link.Errors(), pcie.SignalLinkWidth) {
		m.Device.Link.CurrentWidth = &link.Width
	}
	data, err := readPCIAttribute(ctx, filepath.Join(path, "max_link_speed"))
	if err == nil {
		var speed float64
		speed, err = parsePCILinkSpeed(data)
		if err == nil {
			m.Device.Link.MaxSpeedGTPerSec = &speed
			m.Device.Link.MaxGeneration = pciGeneration(speed)
			if m.Device.Link.MaxGeneration == nil {
				m.AddError(fmt.Errorf("%w: unrecognized PCIe maximum speed %g GT/s", health.ErrSignalUnavailable, speed), pcie.SignalPCIMaxGeneration)
			}
		}
	}
	if err != nil {
		m.AddError(err, pcie.SignalPCIMaxLinkSpeed, pcie.SignalPCIMaxGeneration)
	}
	data, err = readPCIAttribute(ctx, filepath.Join(path, "max_link_width"))
	if err == nil {
		var width int64
		width, err = parsePCICount(strings.TrimSpace(string(data)))
		if err == nil {
			m.Device.Link.MaxWidth = &width
		}
	}
	if err != nil {
		m.AddError(err, pcie.SignalPCIMaxLinkWidth)
	}
	return PCIDeviceInfo{Identity: m, Link: link}
}

func pciSignalFailed(failures []health.ErroredMeasurement, signal string) bool {
	for _, failure := range failures {
		if slices.Contains(failure.FailedSignals(), signal) {
			return true
		}
	}
	return false
}

func pciGeneration(speed float64) *int64 {
	for index, known := range []float64{2.5, 5, 8, 16, 32, 64, 128} {
		if speed == known {
			generation := int64(index + 1)
			return &generation
		}
	}
	return nil
}

func collectPCIProperties(ctx context.Context, path string, m *pcie.PCIeMeasurement) {
	for _, field := range []struct {
		name   string
		signal string
		bits   int
	}{
		{name: "subsystem_vendor", signal: pcie.SignalPCISubsystemVendorID, bits: 16},
		{name: "subsystem_device", signal: pcie.SignalPCISubsystemDeviceID, bits: 16},
		{name: "revision", signal: pcie.SignalPCIRevision, bits: 8},
	} {
		if err := ctx.Err(); err != nil {
			m.AddError(err, field.signal)
			continue
		}
		value, err := readPCIHex(filepath.Join(path, field.name), field.bits)
		if err != nil {
			m.AddError(err, field.signal)
			continue
		}
		switch field.signal {
		case pcie.SignalPCISubsystemVendorID:
			id := uint16(value)
			m.Device.SubsystemVendorID = &id
		case pcie.SignalPCISubsystemDeviceID:
			id := uint16(value)
			m.Device.SubsystemDeviceID = &id
		case pcie.SignalPCIRevision:
			revision := uint8(value)
			m.Device.Revision = &revision
		}
	}
	locality := &pcie.PCILocalityInfo{}
	m.Device.Locality = locality
	data, err := readPCIAttribute(ctx, filepath.Join(path, "numa_node"))
	if err == nil {
		var node int64
		node, err = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err == nil && node < -1 {
			err = fmt.Errorf("invalid NUMA node %d", node)
		}
		if err == nil {
			locality.NUMANode = &node
		}
	}
	if err != nil {
		m.AddError(err, pcie.SignalPCINUMANode)
	}
	data, err = readPCIAttribute(ctx, filepath.Join(path, "local_cpulist"))
	if err == nil {
		cpus := strings.TrimSpace(string(data))
		if cpus != "" {
			_, err = parseSysfsListCount(data)
		}
		if err == nil {
			locality.LocalCPUs = &cpus
		}
	}
	if err != nil {
		m.AddError(err, pcie.SignalPCILocalCPUs)
	}
	driver, err := optionalPCILink(ctx, path, "driver")
	if err != nil {
		m.AddError(err, pcie.SignalPCIDriver)
	} else {
		m.Device.Driver = &driver
	}
	collectPCISRIOV(ctx, path, m)
}

// An absent optional link is known-empty; a dangling link is a collection error.
func optionalPCILink(ctx context.Context, path, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	link := filepath.Join(path, name)
	if _, err := os.Lstat(link); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, deviceErr := os.Stat(path); deviceErr != nil {
				return "", deviceErr
			}
			return "", nil
		}
		return "", err
	}
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", err
	}
	return filepath.Base(target), nil
}

func collectPCISRIOV(ctx context.Context, path string, m *pcie.PCIeMeasurement) {
	info := &pcie.PCISRIOVInfo{VirtualFunctionBDFs: []string{}}
	m.Device.SRIOV = info
	pf, err := optionalPCILink(ctx, path, "physfn")
	if err == nil && pf != "" && !pciBDFPattern.MatchString(pf) {
		err = fmt.Errorf("invalid physical function BDF %q", pf)
	}
	if err != nil {
		m.AddError(err, pcie.SignalPCIPhysicalFunction)
	} else {
		pf = strings.ToLower(pf)
		info.PhysicalFunctionBDF = &pf
	}
	for _, field := range []struct {
		value  **int64
		name   string
		signal string
	}{
		{value: &info.TotalVFs, name: "sriov_totalvfs", signal: pcie.SignalPCITotalVFs},
		{value: &info.EnabledVFs, name: "sriov_numvfs", signal: pcie.SignalPCIEnabledVFs},
	} {
		var data []byte
		data, err = readPCIAttribute(ctx, filepath.Join(path, field.name))
		var count int64
		if err == nil {
			count, err = parsePCICount(strings.TrimSpace(string(data)))
		} else if errors.Is(err, os.ErrNotExist) {
			if _, deviceErr := os.Stat(path); deviceErr == nil {
				err = nil
			}
		}
		if err != nil {
			m.AddError(err, field.signal)
		} else {
			*field.value = &count
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		m.AddError(err, pcie.SignalPCIVirtualFunctions)
		return
	}
	for _, entry := range entries {
		index, ok := strings.CutPrefix(entry.Name(), "virtfn")
		if !ok {
			continue
		}
		if _, err = parseSysfsIndex(index); err != nil {
			m.AddError(err, pcie.SignalPCIVirtualFunctions)
			continue
		}
		var vf string
		vf, err = optionalPCILink(ctx, path, entry.Name())
		if err == nil && !pciBDFPattern.MatchString(vf) {
			err = fmt.Errorf("invalid virtual function BDF %q", vf)
		}
		if err != nil {
			m.AddError(err, pcie.SignalPCIVirtualFunctions)
			continue
		}
		info.VirtualFunctionBDFs = append(info.VirtualFunctionBDFs, strings.ToLower(vf))
	}
	slices.Sort(info.VirtualFunctionBDFs)
}
