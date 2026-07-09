package extractor

import (
	"context"
	"path/filepath"
	"testing"

	"gpu-metrics-monitor/internal/config"
)

func TestColumnarPPU(t *testing.T) {
	cfgPath := filepath.Join("..", "..", "configs", "ppu.example.yaml")
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// ppu-smi dmon -c 1 output, transcribed from kb/ppu.txt. Same nvidia-smi
	// dmon lineage as ixsmi: a double #-prefixed header (names then units),
	// no banner, so skip_lines stays at its default of 0.
	const fixture = `
# ppu   pwr ptemp mtemp    cu  core   mem   enc   dec  mclk  pclk
# idx     W     C     C     %     %     %     %     %   MHz   MHz
    0   147    35    35     0     0    92     0     0  1800  1200
    1   147    37    35     0     0    92     0     0  1800  1650
    2   267    44    49   100   100    95     0     0  1800  1700
    3   135    32    33     0     0    89     0     0  1800   200
    4   114    32    35     0     0    46     0     0  1800   200
    5   115    34    36     0     0    57     0     0  1800   200
    6   176    43    40   100   100    48     0     0  1800  1700
    7   158    38    37     0     0    53     0     0  1800  1700
    8   219    47    50   100   100    91     0     0  1800  1700
    9   225    50    53   100   100    91     0     0  1800  1700
   10   220    49    51   100   100    91     0     0  1800  1700
   11   226    45    47   100   100    91     0     0  1800  1700
   12    86    32    33     0     0    91     0     0  1800   200
   13    87    35    37     0     0    91     0     0  1800   200
   14    83    36    37     0     0    91     0     0  1800   200
   15    81    36    37     0     0    91     0     0  1800   200
`
	pool, err := NewPool(cfg.Metrics)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	points, errs := pool.ExtractAll(context.Background(), fixture)
	for _, e := range errs {
		t.Logf("extract error: %v", e)
	}

	if len(points) == 0 {
		t.Fatal("no metric points extracted")
	}

	for _, p := range points {
		t.Logf("point: %s{device=%s} = %f", p.Name, p.Labels["device"], p.Value)
	}

	// Sample has 16 devices (idx 0..15), every value numeric → 10 metrics × 16.
	expectedPoints := len(cfg.Metrics) * 16
	if len(points) != expectedPoints {
		t.Errorf("got %d points, expect %d (10 metrics × 16 devices)", len(points), expectedPoints)
	}

	// Spot-check every column against device 0 / device 2 values from the sample:
	//   # ppu   pwr ptemp mtemp    cu  core   mem   enc   dec  mclk  pclk
	//       0   147    35    35     0     0    92     0     0  1800  1200
	//       2   267    44    49   100   100    95     0     0  1800  1700
	cases := []struct {
		name   string
		device string
		want   float64
	}{
		{"ppu_power_watts", "0", 147},                 // pwr
		{"ppu_temperature_celsius", "0", 35},          // ptemp
		{"ppu_memory_temperature_celsius", "0", 35},   // mtemp
		{"ppu_compute_utilization_percent", "2", 100}, // cu
		{"ppu_core_utilization_percent", "2", 100},    // core
		{"ppu_memory_utilization_percent", "0", 92},   // mem
		{"ppu_encoder_utilization_percent", "0", 0},   // enc
		{"ppu_decoder_utilization_percent", "0", 0},   // dec
		{"ppu_memory_clock_mhz", "0", 1800},           // mclk
		{"ppu_processor_clock_mhz", "0", 1200},        // pclk
	}
	for _, c := range cases {
		found := false
		for _, p := range points {
			if p.Name == c.name && p.Labels["device"] == c.device {
				if p.Value != c.want {
					t.Errorf("%s device=%s: got %f, want %f", c.name, c.device, p.Value, c.want)
				}
				found = true
			}
		}
		if !found {
			t.Errorf("%s device=%s not found in extracted points", c.name, c.device)
		}
	}

	// Aliases are declared on the four universal metrics; they are expanded by
	// the collector, not the extractor, so only the brand-primary names appear
	// here. Their presence is verified by `validate` (see configs/ppu.example.yaml).
}
