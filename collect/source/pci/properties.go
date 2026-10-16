// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pci

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pcisignals "github.com/dsx-ai-factory/health-validation/collect/signals/pci"
)

var errNotSupported = errors.New("PCI property not supported")

func (s *Source) property(base string, r *Reading) error {
	var field string
	generation := false
	width := false
	switch r.Signal.ID() {
	case pcisignals.LinkSpeed.ID(), pcisignals.LinkGeneration.ID():
		field = "current_link_speed"
		generation = r.Signal.ID() == pcisignals.LinkGeneration.ID()
	case pcisignals.MaxLinkSpeed.ID(), pcisignals.MaxLinkGeneration.ID():
		field = "max_link_speed"
		generation = r.Signal.ID() == pcisignals.MaxLinkGeneration.ID()
	case pcisignals.LinkWidth.ID():
		field, width = "current_link_width", true
	case pcisignals.MaxLinkWidth.ID():
		field, width = "max_link_width", true
	default:
		return fmt.Errorf("unsupported PCI link signal %q", r.Signal.ID())
	}
	r.Path = path.Join(base, field)
	raw, err := s.readFile(r.Path, 128)
	if err == nil {
		value := strings.TrimSpace(string(raw))
		if width {
			var n int64
			n, err = strconv.ParseInt(value, 10, 64)
			if err == nil && n < -1 {
				err = ErrMalformed
			}
			if err == nil {
				r.Value = &observation.Value{Kind: &observation.Value_IntValue{IntValue: n}}
			}
		} else {
			r.Value, err = linkSpeed(value, generation)
		}
		if err != nil && !errors.Is(err, errNotSupported) {
			err = fmt.Errorf("%w: %v", ErrMalformed, err)
		}
	}
	// Missing attributes on a surviving device are unsupported; dangling links
	// and devices that disappear during collection remain errors.
	if errors.Is(err, fs.ErrNotExist) {
		absent, checkErr := s.absent(base, field)
		if checkErr != nil {
			err = checkErr
		} else if absent {
			err = errNotSupported
		}
	}
	if errors.Is(err, errNotSupported) {
		r.NoValue = &observation.NoValue{Reason: observation.NoValueReasonNotSupported, Detail: err.Error()}
		return nil
	}
	return err
}

func (s *Source) existing(name string) (string, error) {
	resolved, err := s.resolve(name)
	if err == nil {
		_, err = fs.Stat(s.files, resolved)
	}
	return resolved, err
}

func (s *Source) absent(base, field string) (bool, error) {
	resolved, err := s.existing(base)
	if err != nil {
		return false, err
	}
	entries, err := fs.ReadDir(s.files, resolved)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() == field {
			return false, nil
		}
	}
	return true, nil
}

func linkSpeed(value string, generation bool) (*observation.Value, error) {
	if value == "Unknown" || value == "Unknown speed" {
		return nil, fmt.Errorf("%w: link speed is %s", errNotSupported, value)
	}
	fields := strings.Fields(value)
	if len(fields) < 2 || fields[1] != "GT/s" || (len(fields) != 2 && (len(fields) != 3 || fields[2] != "PCIe")) {
		return nil, ErrMalformed
	}
	// Decode only the defined PCI generations; never guess from a speed.
	speeds := []float64{2.5, 5, 8, 16, 32, 64, 128}
	n, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, ErrMalformed
	}
	if generation {
		for i, speed := range speeds {
			if n == speed {
				return number(uint64(i + 1)), nil
			}
		}
		return nil, fmt.Errorf("%w: no PCI generation for %s", errNotSupported, value)
	}
	return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: n}}, nil
}
