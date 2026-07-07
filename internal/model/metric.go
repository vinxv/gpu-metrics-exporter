package model

// MetricPoint is the intermediate representation between extraction and
// Prometheus metric generation. Each point represents one labeled data value.
type MetricPoint struct {
	Name   string            // Prometheus metric name
	Help   string            // HELP description text
	Type   string            // "gauge" or "counter"
	Value  float64           // numeric value
	Labels map[string]string // label name => value (must include "device")
}

// valid metric types
const (
	TypeGauge   = "gauge"
	TypeCounter = "counter"
)
