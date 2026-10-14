// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie

import "github.com/dsx-ai-factory/health-validation/catalog/health"

func baseCheck(id, name, description string, signals ...string) health.CheckDefinition {
	return health.CheckDefinition{
		ID: id, Name: name, Description: description,
		Category: health.PCIe, RequiredSignals: signals,
		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		SchedulingCadence: health.Continuous, ConditionScope: health.NodeScope,
		Provenance: health.NVIDIA, MaturityLevel: health.Experimental,
	}
}
