// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func collectPCIIsolation(ctx context.Context, path string, m *pcie.PCIeMeasurement) {
	group, err := readPCIIOMMU(ctx, path)
	m.Device.IOMMU = group
	if err != nil {
		failedSignals := []string{pcie.SignalIOMMUGroupDevices}
		if group == nil {
			failedSignals = append(failedSignals, pcie.SignalIOMMUGroup)
		}
		m.AddError(fmt.Errorf("PCIe %s IOMMU: %w", m.Device.BDF, err), failedSignals...)
	}
	acs, err := readPCIACS(ctx, filepath.Join(path, "config"))
	if err != nil {
		m.AddError(fmt.Errorf("PCIe %s ACS: %w", m.Device.BDF, err), pcie.SignalACSSupported, pcie.SignalACSCapabilities, pcie.SignalACSControl)
	} else {
		m.Device.ACS = acs
	}
}

func readPCIIOMMU(ctx context.Context, path string) (*pcie.IOMMUInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	link := filepath.Join(path, "iommu_group")
	if _, err := os.Lstat(link); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, deviceErr := os.Stat(path); deviceErr != nil {
				return nil, deviceErr
			}
			return &pcie.IOMMUInfo{GroupID: -1, Devices: []string{}}, nil
		}
		return nil, err
	}
	groupPath, err := filepath.EvalSymlinks(link)
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseUint(filepath.Base(groupPath), 10, 63)
	if err != nil {
		return nil, fmt.Errorf("invalid IOMMU group path %s: %w", groupPath, err)
	}
	group := &pcie.IOMMUInfo{GroupID: int64(id), Devices: []string{}}
	entries, err := os.ReadDir(filepath.Join(groupPath, "devices"))
	if err != nil {
		return group, err
	}
	for _, entry := range entries {
		if pciBDFPattern.MatchString(entry.Name()) {
			group.Devices = append(group.Devices, strings.ToLower(entry.Name()))
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return group, nil
}

func readPCIACS(ctx context.Context, path string) (*pcie.ACSInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", health.ErrSignalUnavailable, err)
	}
	defer f.Close()
	// Read only extended-capability headers and the ACS registers.
	seen := make(map[int64]bool)
	for offset := int64(0x100); offset != 0; {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if offset < 0x100 || offset > 0xffc || offset%4 != 0 || seen[offset] {
			return nil, fmt.Errorf("invalid or cyclic PCI extended capability offset %#x", offset)
		}
		seen[offset] = true
		var data [4]byte
		if _, err = f.ReadAt(data[:], offset); err != nil {
			return nil, fmt.Errorf("%w: read capability at %#x: %w", health.ErrSignalUnavailable, offset, err)
		}
		header := binary.LittleEndian.Uint32(data[:])
		if header == 0 {
			return &pcie.ACSInfo{}, nil
		}
		if header == 0xffffffff {
			return nil, fmt.Errorf("%w: PCI configuration space returned all ones", health.ErrSignalUnavailable)
		}
		if (header>>16)&0xf == 0 {
			return nil, fmt.Errorf("invalid PCI extended capability version at %#x", offset)
		}
		if header&0xffff == 0x000d {
			if offset > 0xff8 {
				return nil, fmt.Errorf("ACS registers exceed PCI configuration space")
			}
			if _, err = f.ReadAt(data[:], offset+4); err != nil {
				return nil, fmt.Errorf("%w: read ACS registers: %w", health.ErrSignalUnavailable, err)
			}
			return &pcie.ACSInfo{Supported: true, Capabilities: binary.LittleEndian.Uint16(data[:2]), Control: binary.LittleEndian.Uint16(data[2:])}, nil
		}
		offset = int64(header >> 20)
	}
	return &pcie.ACSInfo{}, nil
}
