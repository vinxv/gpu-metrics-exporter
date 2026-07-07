package extractor

import (
	"context"
	"fmt"

	"gpu-metrics-monitor/internal/config"
	"gpu-metrics-monitor/internal/model"
)

// Extractor parses smi stdout into metric points.
type Extractor interface {
	Extract(ctx context.Context, stdout string) ([]model.MetricPoint, error)
}

// Kind constants mirror config.Kind* for dispatch clarity.
const (
	KindColumnar = config.KindColumnar
	KindRegex    = config.KindRegex
	KindJS       = config.KindJS
)

// New creates an Extractor for the given metric definition.
func New(md config.MetricDef) (Extractor, error) {
	switch md.Kind {
	case KindColumnar:
		return newColumnar(md)
	case KindRegex:
		return newRegex(md)
	case KindJS:
		return newJSVM(md)
	default:
		return nil, fmt.Errorf("unknown extract kind: %q", md.Kind)
	}
}

// Pool holds a pre-created extractor per metric.
// All extractors in the pool receive the same smi stdout and produce
// their respective metric points.
type Pool struct {
	extractors []Extractor
}

// NewPool creates extractors for all configured metrics.
func NewPool(metrics []config.MetricDef) (*Pool, error) {
	extractors := make([]Extractor, 0, len(metrics))
	for _, md := range metrics {
		e, err := New(md)
		if err != nil {
			return nil, fmt.Errorf("create extractor for %q: %w", md.Name, err)
		}
		extractors = append(extractors, e)
	}
	return &Pool{extractors: extractors}, nil
}

// ExtractAll runs all extractors against the same stdout.
// A single extractor failing does not stop the others — the error is logged
// via the caller. Partial results are returned.
func (p *Pool) ExtractAll(ctx context.Context, stdout string) ([]model.MetricPoint, []error) {
	var (
		allPoints []model.MetricPoint
		allErrs   []error
	)

	for _, e := range p.extractors {
		points, err := e.Extract(ctx, stdout)
		if err != nil {
			allErrs = append(allErrs, err)
			continue
		}
		allPoints = append(allPoints, points...)
	}

	return allPoints, allErrs
}
