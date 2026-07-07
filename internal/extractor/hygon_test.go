package extractor

import (
	"context"
	"path/filepath"
	"testing"

	"gpu-metrics-monitor/internal/config"
)

func TestColumnarHygon(t *testing.T) {
	cfgPath := filepath.Join("..", "..", "configs", "hygon.example.yaml")
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	const fixture = ` hy-smi

================================= System Management Interface ==================================
================================================================================================
HCU     Temp     AvgPwr     Perf     PwrCap     VRAM%      HCU%      Mode
0       62.0C    111.0W     auto     400.0W     94%        0.0%      Normal
1       65.0C    110.0W     auto     400.0W     94%        0.0%      Normal
2       67.0C    106.0W     auto     400.0W     94%        0.0%      Normal
3       65.0C    111.0W     auto     400.0W     94%        0.0%      Normal
4       59.0C    109.0W     auto     400.0W     94%        0.0%      Normal
5       60.0C    107.0W     auto     400.0W     94%        0.0%      Normal
6       58.0C    106.0W     auto     400.0W     94%        0.0%      Normal
7       57.0C    109.0W     auto     400.0W     93%        0.0%      Normal
================================================================================================
======================================== End of SMI Log ========================================
`
	pool, err := NewPool(cfg.Metrics)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	points, errs := pool.ExtractAll(context.Background(), fixture)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Logf("extract error: %v", e)
		}
	}

	if len(points) == 0 {
		t.Fatal("no metric points extracted")
	}

	for _, p := range points {
		t.Logf("point: %s{device=%s} = %f", p.Name, p.Labels["device"], p.Value)
	}

	// Spot-check: hcu_temperature_celsius for device 0 should be 62.0
	found := false
	for _, p := range points {
		if p.Name == "hcu_temperature_celsius" && p.Labels["device"] == "0" {
			if p.Value != 62.0 {
				t.Errorf("hcu_temperature_celsius device=0: got %f, want 62.0", p.Value)
			}
			found = true
		}
	}
	if !found {
		t.Error("hcu_temperature_celsius device=0 not found")
	}

	// Spot-check: hcu_power_watts for device 0 should be 111.0
	for _, p := range points {
		if p.Name == "hcu_power_watts" && p.Labels["device"] == "0" {
			if p.Value != 111.0 {
				t.Errorf("hcu_power_watts device=0: got %f, want 111.0", p.Value)
			}
		}
	}

	// 5 metrics × 8 devices = 40 points
	expectedPoints := len(cfg.Metrics) * 8
	if len(points) != expectedPoints {
		t.Errorf("got %d points, expect %d (5 metrics × 8 devices)", len(points), expectedPoints)
	}
}
