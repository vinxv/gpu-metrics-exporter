package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dop251/goja"

	"gpu-metrics-monitor/internal/config"
	"gpu-metrics-monitor/internal/model"
)

// jsvmExtractor runs a user-provided JS script in a goja sandbox.
// The script receives the smi stdout as `input` and calls `emit()` to
// produce metric points.
type jsvmExtractor struct {
	metricDef config.MetricDef
	jc        config.JSConfig
}

func newJSVM(md config.MetricDef) (*jsvmExtractor, error) {
	return &jsvmExtractor{
		metricDef: md,
		jc:        *md.JS,
	}, nil
}

func (j *jsvmExtractor) Extract(ctx context.Context, stdout string) (points []model.MetricPoint, err error) {
	// Recover from JS panics (infinite loops trigger InterruptMode panic).
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("jsvm %q: script panic: %v", j.metricDef.Name, r)
		}
	}()

	vm := goja.New()

	// Collect emitted metric points into a hidden slice.
	var collected []model.MetricPoint

	// Expose emit(name, value, labels) to JS.
	if err := vm.Set("emit", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			slog.Warn("jsvm emit: need at least 2 args (name, value)", "metric", j.metricDef.Name)
			return goja.Undefined()
		}

		name := call.Arguments[0].String()
		val := call.Arguments[1].ToFloat()

		labels := make(map[string]string)
		if len(call.Arguments) >= 3 && !goja.IsUndefined(call.Arguments[2]) && !goja.IsNull(call.Arguments[2]) {
			obj := call.Arguments[2].ToObject(vm)
			for _, key := range obj.Keys() {
				labels[key] = obj.Get(key).String()
			}
		}

		collected = append(collected, model.MetricPoint{
			Name:   name,
			Help:   j.metricDef.Help,
			Type:   j.metricDef.Type,
			Value:  val,
			Labels: labels,
		})
		return goja.Undefined()
	}); err != nil {
		return nil, fmt.Errorf("jsvm %q: set emit: %w", j.metricDef.Name, err)
	}

	// Expose log(msg) to JS — forwarded to slog.Debug.
	if err := vm.Set("log", func(call goja.FunctionCall) goja.Value {
		msg := call.Arguments[0].String()
		slog.Debug("jsvm", "metric", j.metricDef.Name, "msg", msg)
		return goja.Undefined()
	}); err != nil {
		return nil, fmt.Errorf("jsvm %q: set log: %w", j.metricDef.Name, err)
	}

	// Set the input data.
	if err := vm.Set("input", stdout); err != nil {
		return nil, fmt.Errorf("jsvm %q: set input: %w", j.metricDef.Name, err)
	}

	// Hard timeout via interrupt: if the script runs too long, interrupt it.
	time.AfterFunc(5*time.Second, func() {
		vm.Interrupt("execution timeout")
	})
	vm.ClearInterrupt()

	_, runErr := vm.RunString(j.jc.Script)
	if runErr != nil {
		// If interrupted, return what we collected so far plus the error.
		if strings.Contains(runErr.Error(), "interrupt") || strings.Contains(runErr.Error(), "timeout") {
			return collected, fmt.Errorf("jsvm %q: script interrupted: %w", j.metricDef.Name, runErr)
		}
		return collected, fmt.Errorf("jsvm %q: script error: %w", j.metricDef.Name, runErr)
	}

	// Override name/help/type from config for consistency, since emit() only
	// sets name (we allow override so JS can emit multiple metrics if needed).
	_ = collected // points are returned as-is from JS

	return collected, nil
}
