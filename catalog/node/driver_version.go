package node

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// SignalDriverVersion is the signal a [DriverVersionMeasurement] carries.
const SignalDriverVersion = "driver_version"

// DriverVersionMeasurement reports the NVIDIA driver version the node is
// running, as the host reported it (e.g. "575.51.03").
//
// It is a measurement of its own rather than a field on the measurements
// whose meaning depends on it. The version is a fact about the node, so it
// is collected once and appears once in a run's output, and any
// accumulator that needs it takes it as an input alongside the
// measurement it interprets.
type DriverVersionMeasurement struct {
	Version string `json:"driver_version"`
	errors  []health.ErroredMeasurement
}

// ErroredDriverVersion returns a DriverVersionMeasurement marked errored:
// the version could not be read.
func ErroredDriverVersion(err error) DriverVersionMeasurement {
	var m DriverVersionMeasurement
	m.errors = append(m.errors, health.NewErroredMeasurement(err, SignalDriverVersion))
	return m
}

// Parsed returns the version for comparison, or the zero value — unknown
// — when the measurement errored or the reported text is not a version.
func (m DriverVersionMeasurement) Parsed() health.DriverVersion {
	if len(m.errors) > 0 {
		return health.DriverVersion{}
	}
	v, _ := health.ParseDriverVersion(m.Version)
	return v
}

// Category implements [health.Measurement].
func (DriverVersionMeasurement) Category() health.Category { return health.Node }

// Errors implements [health.Measurement].
func (m DriverVersionMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
