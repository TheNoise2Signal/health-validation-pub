// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

func collectPCILink(ctx context.Context, path, bdf string) pcie.LinkMeasurement {
	m := pcie.LinkMeasurement{BDF: bdf}
	data, err := readPCIAttribute(ctx, filepath.Join(path, "current_link_speed"))
	if err == nil {
		m.SpeedGTPerSec, err = parsePCILinkSpeed(data)
	}
	if err != nil {
		m.AddError(fmt.Errorf("PCIe %s link speed: %w", bdf, err), pcie.SignalLinkSpeed)
	}
	data, err = readPCIAttribute(ctx, filepath.Join(path, "current_link_width"))
	if err == nil {
		m.Width, err = parsePCICount(strings.TrimSpace(string(data)))
	} else {
		err = fmt.Errorf("%w: %w", health.ErrSignalUnavailable, err)
	}
	if err != nil {
		m.AddError(fmt.Errorf("PCIe %s link width: %w", bdf, err), pcie.SignalLinkWidth)
	}
	return m
}

func collectPCIErrorCounters(ctx context.Context, path, bdf string) (pcie.ErrorMeasurement, bool) {
	m := pcie.ErrorMeasurement{BDF: bdf}
	missing := 0
	for _, attribute := range []struct {
		value  *int64
		name   string
		total  string
		signal string
	}{
		{value: &m.CorrectableErrors, name: "aer_dev_correctable", total: "TOTAL_ERR_COR", signal: pcie.SignalCorrectableErrors},
		{value: &m.NonfatalErrors, name: "aer_dev_nonfatal", total: "TOTAL_ERR_NONFATAL", signal: pcie.SignalNonfatalErrors},
		{value: &m.FatalErrors, name: "aer_dev_fatal", total: "TOTAL_ERR_FATAL", signal: pcie.SignalFatalErrors},
	} {
		data, err := readPCIAttribute(ctx, filepath.Join(path, attribute.name))
		if err == nil {
			*attribute.value, err = parsePCIAERTotal(data, attribute.total)
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing++
			}
			m.AddError(fmt.Errorf("PCIe %s %s: %w", bdf, attribute.name, err), attribute.signal)
		}
	}
	// Linux omits all AER statistics for unsupported devices. Keep partial
	// failures and disappearing devices visible as collection errors.
	if missing == 3 && ctx.Err() == nil {
		if _, err := os.Stat(path); err == nil {
			return m, false
		}
	}
	return m, true
}

func readPCIAttribute(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

func parsePCILinkSpeed(data []byte) (float64, error) {
	fields := strings.Fields(string(data))
	if len(fields) < 2 || fields[1] != "GT/s" {
		return 0, fmt.Errorf("invalid PCIe speed %q: expected GT/s", strings.TrimSpace(string(data)))
	}
	speed, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(speed) || math.IsInf(speed, 0) || speed < 0 {
		return 0, fmt.Errorf("invalid PCIe speed %q", fields[0])
	}
	return speed, nil
}

func parsePCICount(text string) (int64, error) {
	value, err := strconv.ParseUint(text, 10, 63)
	if err != nil {
		return 0, fmt.Errorf("invalid nonnegative PCIe count %q: %w", text, err)
	}
	return int64(value), nil
}

// Use reported AER message totals; summing error-bit rows can double-count messages.
func parsePCIAERTotal(data []byte, totalName string) (int64, error) {
	var count int64
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != totalName {
			continue
		}
		if found || len(fields) != 2 {
			return 0, fmt.Errorf("invalid or duplicate AER total %s", totalName)
		}
		value, err := parsePCICount(fields[1])
		if err != nil {
			return 0, fmt.Errorf("parse AER total %s: %w", totalName, err)
		}
		count, found = value, true
	}
	if !found {
		return 0, fmt.Errorf("AER total %s is missing", totalName)
	}
	return count, nil
}
