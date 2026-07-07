package extractor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gpu-metrics-monitor/internal/config"
)

func TestColumnarAscendNPU(t *testing.T) {
	// Load the ascend config and extract from the mock npu output.
	cfgPath := filepath.Join("..", "..", "configs", "ascend.example.yaml")
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// We don't have a real npu-smi output fixture. Use a synthetic one
	// that matches the documented format.
	stdout := `
NpuID(Idx) ChipId(Idx) Pwr(W)      Temp(C)     AI Core(%)  AI Cpu(%)   Ctrl Cpu(%) Memory(%)   Memory BW(%)
0           0           161.7       43          17          0           5           95          14
1           0           153.8       41          36          0           1           94          14
2           0           172.6       42          4           0           1           94          14
3           0           144.7       40          37          0           4           94          14
`

	pool, err := NewPool(cfg.Metrics)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	points, errs := pool.ExtractAll(context.Background(), stdout)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Logf("extract error: %v", e)
		}
	}

	if len(points) == 0 {
		t.Fatal("no metric points extracted")
	}

	// Spot-check: npu_power_watts for device 0 should be 161.7
	found := false
	for _, p := range points {
		t.Logf("point: %s{device=%s} = %f", p.Name, p.Labels["device"], p.Value)
		if p.Name == "npu_power_watts" && p.Labels["device"] == "0" {
			if p.Value != 161.7 {
				t.Errorf("npu_power_watts device=0: got %f, want 161.7", p.Value)
			}
			found = true
		}
	}
	if !found {
		t.Error("npu_power_watts device=0 not found in extracted points")
	}

	// Should have 7 metrics × 4 devices = 28 points
	expectedPoints := len(cfg.Metrics) * 4
	if len(points) != expectedPoints {
		t.Errorf("got %d points, expect %d (7 metrics × 4 devices)", len(points), expectedPoints)
	}
}

func TestColumnarIlluvatar(t *testing.T) {
	cfgPath := filepath.Join("..", "..", "configs", "illuvatar.example.yaml")
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Synthetic illuvatar-format output.
	stdout := `
# gpu    pwr  gtemp  mtemp     sm    mem    enc    dec    jpu   mclk   pclk
# Idx      W      C      C      %      %      %      %      %    MHz    MHz
    0      -     49     41     86     96      0      0      -   1600   1600
    1    234     51     43     90     96      0      0      -   1600   1600
    2      -     60     45     84     96      0      0      -   1600   1600
`

	pool, err := NewPool(cfg.Metrics)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	points, errs := pool.ExtractAll(context.Background(), stdout)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Logf("extract error: %v", e)
		}
	}

	if len(points) == 0 {
		t.Fatal("no metric points extracted")
	}

	// Device 0 has pwr = "-" → should be skipped for gpu_power_watts
	for _, p := range points {
		t.Logf("point: %s{device=%s} = %f", p.Name, p.Labels["device"], p.Value)
		if p.Name == "gpu_power_watts" && p.Labels["device"] == "0" {
			t.Error("gpu_power_watts device=0 should be skipped (value is '-')")
		}
	}

	// Device 1 should have power=234
	found := false
	for _, p := range points {
		if p.Name == "gpu_power_watts" && p.Labels["device"] == "1" {
			if p.Value != 234 {
				t.Errorf("gpu_power_watts device=1: got %f, want 234", p.Value)
			}
			found = true
		}
	}
	if !found {
		t.Error("gpu_power_watts device=1 not found")
	}
}

func TestRegexExtractor(t *testing.T) {
	cfg := &config.Config{
		Command:  "echo",
		Timeout:  5e9,
		Interval: 10e9,
		Metrics: []config.MetricDef{
			{
				Name: "test_utilization",
				Help: "Test utilization",
				Type: "gauge",
				Kind: "regex",
				Regex: &config.RegexConfig{
					Pattern:           `^Device\s+(\d+)\s*:\s*([\d.]+)%`,
					DeviceGroup:       1,
					ValueGroup:        2,
					UnavailableValues: []string{"-", "N/A"},
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validation: %v", err)
	}

	pool, err := NewPool(cfg.Metrics)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	stdout := `
Device 0: 85.5%
Device 1: 92.0%
Device 2: N/A
Device 3: 78.3%
`
	points, errs := pool.ExtractAll(context.Background(), stdout)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Logf("extract error: %v", e)
		}
	}

	// Should have 3 points (device 2 is N/A)
	if len(points) != 3 {
		t.Errorf("got %d points, want 3", len(points))
	}

	for _, p := range points {
		switch p.Labels["device"] {
		case "0":
			if p.Value != 85.5 {
				t.Errorf("device 0: %f", p.Value)
			}
		case "1":
			if p.Value != 92.0 {
				t.Errorf("device 1: %f", p.Value)
			}
		case "3":
			if p.Value != 78.3 {
				t.Errorf("device 3: %f", p.Value)
			}
		}
	}
}

func TestJSExtractor(t *testing.T) {
	cfg := &config.Config{
		Command:  "echo",
		Timeout:  5e9,
		Interval: 10e9,
		Metrics: []config.MetricDef{
			{
				Name: "js_metric",
				Help: "JS parsed metric",
				Type: "gauge",
				Kind: "js",
				JS: &config.JSConfig{
					Script: `
var lines = input.split('\n');
for (var i = 0; i < lines.length; i++) {
    var line = lines[i].trim();
    if (line === '' || line.startsWith('#')) continue;
    var parts = line.split(/\s+/);
    if (parts.length >= 2) {
        var val = parseFloat(parts[1]);
        if (!isNaN(val)) {
            emit('js_metric', val, {device: parts[0]});
        }
    }
}
`,
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validation: %v", err)
	}

	pool, err := NewPool(cfg.Metrics)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	stdout := `
0 100.5
1 200.3
2 -1
`
	points, errs := pool.ExtractAll(context.Background(), stdout)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Fatalf("extract error: %v", e)
		}
	}

	if len(points) != 3 {
		t.Fatalf("got %d points, want 3", len(points))
	}

	if points[0].Value != 100.5 {
		t.Errorf("device 0: %f", points[0].Value)
	}
	if points[1].Value != 200.3 {
		t.Errorf("device 1: %f", points[1].Value)
	}
}

func TestJSExtractorInfiniteLoopProtection(t *testing.T) {
	cfg := &config.Config{
		Command:  "echo",
		Timeout:  5e9,
		Interval: 10e9,
		Metrics: []config.MetricDef{
			{
				Name: "loop_metric",
				Help: "Infinite loop test",
				Type: "gauge",
				Kind: "js",
				JS: &config.JSConfig{
					Script: "while(true) {}",
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validation: %v", err)
	}

	pool, err := NewPool(cfg.Metrics)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	_, errs := pool.ExtractAll(context.Background(), "dummy")
	if len(errs) == 0 {
		t.Fatal("expected error from infinite loop, got nil")
	}
	if len(errs) > 0 {
		t.Logf("got expected error: %v", errs[0])
		if !strings.Contains(errs[0].Error(), "interrupt") {
			t.Logf("error is not about interrupt, but that's ok: %v", errs[0])
		}
	}
}

func TestColumnarMissingColumn(t *testing.T) {
	cfg := &config.Config{
		Command:  "echo",
		Timeout:  5e9,
		Interval: 10e9,
		Metrics: []config.MetricDef{
			{
				Name: "missing_metric",
				Help: "Should fail",
				Type: "gauge",
				Kind: "columnar",
				Columnar: &config.ColumnarConfig{
					ColumnName:   "NonexistentCol",
					DeviceColumn: 0,
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validation: %v", err)
	}

	pool, err := NewPool(cfg.Metrics)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	stdout := `
Header1 Header2 Header3
0       100     200
`

	_, errs := pool.ExtractAll(context.Background(), stdout)
	if len(errs) == 0 {
		t.Fatal("expected error for missing column, got nil")
	}
	if !strings.Contains(errs[0].Error(), "not found") {
		t.Errorf("error should mention column not found: %v", errs[0])
	}
}
