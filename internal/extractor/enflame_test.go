package extractor

import (
	"context"
	"path/filepath"
	"testing"

	"gpu-metrics-monitor/internal/config"
)

func TestColumnarEnflame(t *testing.T) {
	cfgPath := filepath.Join("..", "..", "configs", "enflame.example.yaml")
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	const fixture = `-------------------------------------------------------------------------------
--------------------- Enflame System Management Interface ---------------------
--------- Enflame Tech, All Rights Reserved. 2024-2025 Copyright (C) ----------
-------------------------------------------------------------------------------

*Dev Pwr    DTemp  Sip    DUsed  Dpm     MUsed  Mem    Mclk   TxPci   RxPci
*Idx W      C      %      %      L       %      MiB    MHz    MiB/s   MiB/s
0    101    36     0.0    0.0    Sleep   84.1   42976  7000   0       0
1    98     37     0.0    0.0    Sleep   83.7   42976  7000   0       0
2    101    37     0.0    0.0    Sleep   84.5   42976  7000   0       0
3    101    37     0.0    0.0    Sleep   84.2   42976  7000   0       0
4    101    37     0.0    0.0    Sleep   2.6    42976  7000   0       0
5    100    34     0.0    0.0    Sleep   2.6    42976  7000   0       0
6    100    34     0.0    0.0    Sleep   2.6    42976  7000   0       0
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

	// Spot-check: gcu_power_watts for device 0 should be 101
	found := false
	for _, p := range points {
		if p.Name == "gcu_power_watts" && p.Labels["device"] == "0" {
			if p.Value != 101 {
				t.Errorf("gcu_power_watts device=0: got %f, want 101", p.Value)
			}
			found = true
		}
	}
	if !found {
		t.Error("gcu_power_watts device=0 not found")
	}

	// device 4: MUsed = 2.6
	for _, p := range points {
		if p.Name == "gcu_memory_utilization_percent" && p.Labels["device"] == "4" {
			if p.Value != 2.6 {
				t.Errorf("gcu_memory_utilization_percent device=4: got %f, want 2.6", p.Value)
			}
		}
	}

	// 9 metrics × 7 devices = 63 points
	expectedPoints := len(cfg.Metrics) * 7
	if len(points) != expectedPoints {
		t.Errorf("got %d points, expect %d (9 metrics × 7 devices)", len(points), expectedPoints)
	}
}
