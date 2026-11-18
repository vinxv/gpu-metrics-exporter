package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gpu-metrics-monitor/internal/collector"
	"gpu-metrics-monitor/internal/config"
)

func metricsHandler(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	col, err := collector.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, col).http.Handler
}

func scrapeMetrics(t *testing.T, handler http.Handler) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("scrape status %d:\n%s", response.Code, response.Body)
	}
	return response.Body.String()
}

func TestMetricsCachedScrapeIncludesSelfMetricsAndJSLabels(t *testing.T) {
	cfg, err := config.LoadBytes([]byte(`
command: "printf '42'"
timeout: 5s
interval: 1m
metrics:
  - name: test_temperature
    help: Test temperature.
    type: gauge
    kind: js
    aliases: [device_temperature]
    js:
      label_names: [zone, chip]
      script: |
        emit("test_temperature", Number(input), {chip: "1", device: "0", zone: "core"});
        emit("test_temperature", 99, {device: "0", zone: "core"});
`))
	if err != nil {
		t.Fatal(err)
	}
	handler := metricsHandler(t, cfg)
	first := scrapeMetrics(t, handler)
	for _, want := range []string{
		"gpu_metrics_up 1\n",
		"gpu_metrics_scrape_duration_seconds ",
		"gpu_metrics_parse_errors_total 1\n",
		`test_temperature{chip="1",device="0",zone="core"} 42`,
		`device_temperature{chip="1",device="0",zone="core"} 42`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first scrape missing %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, " 99\n") {
		t.Errorf("first scrape included the sample missing a declared label:\n%s", first)
	}
	if cached := scrapeMetrics(t, handler); cached != first {
		t.Errorf("cached scrape differs from first scrape:\n%s", cached)
	}
}

func TestMetricsAscendTwoChipsOnSameDevice(t *testing.T) {
	cfg, err := config.LoadFile(filepath.Join("..", "..", "configs", "ascend.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Command = `cat <<'EOF'
NpuID(Idx) ChipId(Idx) Pwr(W)      Temp(C)     AI Core(%)  AI Cpu(%)   Ctrl Cpu(%) Memory(%)   Memory BW(%)
0           0           161.7       43          17          0           5           95          14
0           1           153.8       40          36          0           1           94          14
EOF`
	body := scrapeMetrics(t, metricsHandler(t, cfg))
	for _, want := range []string{
		`npu_temperature_celsius{brand="ascend",chip="0",device="0"} 43`,
		`npu_temperature_celsius{brand="ascend",chip="1",device="0"} 40`,
		`device_temperature_celsius{brand="ascend",chip="0",device="0"} 43`,
		`device_temperature_celsius{brand="ascend",chip="1",device="0"} 40`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q:\n%s", want, body)
		}
	}
}
