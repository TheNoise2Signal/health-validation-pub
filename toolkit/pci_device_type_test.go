// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"encoding/json"
	"testing"
)

func TestPCIDeviceTypeJSON(t *testing.T) {
	for value, name := range pciDeviceTypeNames {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `"`+name+`"` || value.String() != name {
			t.Errorf("type %d: JSON=%s, String=%s", value, data, value.String())
		}
		var decoded PCIDeviceType
		if err = json.Unmarshal(data, &decoded); err != nil || decoded != value {
			t.Errorf("round trip: value=%d, error=%v", decoded, err)
		}
	}
	if _, err := json.Marshal(PCIDeviceType(99)); err == nil {
		t.Error("invalid enum value accepted")
	}
	for _, input := range []string{`"invalid"`, `3`, `null`} {
		var value PCIDeviceType
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("invalid JSON accepted: %s", input)
		}
	}
}
