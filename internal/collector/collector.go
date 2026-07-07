package collector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"gpu-metrics-monitor/internal/config"
	"gpu-metrics-monitor/internal/executor"
	"gpu-metrics-monitor/internal/extractor"
	"gpu-metrics-monitor/internal/model"
)

// GPUCollector implements prometheus.Collector for GPU metrics.
// It orchestrates smi command execution, output parsing, and metric exposure.
type GPUCollector struct {
	cfg  *config.Config
	cmd  *executor.Command
	pool *extractor.Pool

	mu         sync.RWMutex
	cache      []prometheus.Metric
	lastScrape time.Time

	// Pre-built descriptors keyed by metric name (primary + alias).
	descs   map[string]*prometheus.Desc
	aliases map[string][]string // primary name -> alias names

	// Self-monitoring metrics.
	upGauge          prometheus.Gauge
	scrapeDuration   prometheus.Gauge
	parseErrorsTotal prometheus.Counter
}

// New creates a GPUCollector.
func New(cfg *config.Config) (*GPUCollector, error) {
	cmd := executor.New(cfg.Command, cfg.Timeout)

	pool, err := extractor.NewPool(cfg.Metrics)
	if err != nil {
		return nil, fmt.Errorf("create extractor pool: %w", err)
	}

	c := &GPUCollector{
		cfg:     cfg,
		cmd:     cmd,
		pool:    pool,
		descs:   make(map[string]*prometheus.Desc),
		aliases: make(map[string][]string),
		upGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gpu_metrics_up",
			Help: "Whether the last GPU metrics scrape was successful (1=yes, 0=no).",
		}),
		scrapeDuration: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gpu_metrics_scrape_duration_seconds",
			Help: "Duration of the last GPU metrics scrape in seconds.",
		}),
		parseErrorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gpu_metrics_parse_errors_total",
			Help: "Total number of metric parse errors across all scrapes.",
		}),
	}

	// Pre-build descriptors. Global labels + per-metric labels are const labels;
	// "device" is the only variable label. Per-metric labels override globals.
	for _, md := range cfg.Metrics {
		constLabels := prometheus.Labels{}
		for _, lbl := range cfg.Labels {
			constLabels[lbl.Name] = lbl.Value
		}
		for _, lbl := range md.Labels {
			constLabels[lbl.Name] = lbl.Value
		}
		c.descs[md.Name] = prometheus.NewDesc(
			md.Name,
			md.Help,
			[]string{"device"}, // variable labels
			constLabels,        // const labels from config
		)

		// Build descriptors and record mappings for aliases.
		for _, alias := range md.Aliases {
			c.descs[alias] = prometheus.NewDesc(
				alias,
				md.Help,
				[]string{"device"},
				constLabels,
			)
		}
		if len(md.Aliases) > 0 {
			c.aliases[md.Name] = md.Aliases
		}
	}

	return c, nil
}

// Describe sends descriptors for all configured metrics plus self-monitoring.
func (c *GPUCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descs {
		ch <- desc
	}
	c.upGauge.Describe(ch)
	c.scrapeDuration.Describe(ch)
	c.parseErrorsTotal.Describe(ch)
}

// Collect executes a scrape (if the interval has elapsed) and sends metrics.
func (c *GPUCollector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()

	// Fast path: return cached data if within the configured interval.
	c.mu.RLock()
	if time.Since(c.lastScrape) < c.cfg.Interval && len(c.cache) > 0 {
		for _, m := range c.cache {
			ch <- m
		}
		c.mu.RUnlock()
		return
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-checked locking.
	if time.Since(c.lastScrape) < c.cfg.Interval && len(c.cache) > 0 {
		for _, m := range c.cache {
			ch <- m
		}
		return
	}

	ctx := context.Background()
	stdout, err := c.cmd.Run(ctx)

	duration := time.Since(start).Seconds()
	c.scrapeDuration.Set(duration)

	if err != nil {
		slog.Error("smi command failed", "error", err)
		c.upGauge.Set(0)
		c.sendSelfMetrics(ch)
		return
	}

	points, errs := c.pool.ExtractAll(ctx, stdout)
	for _, e := range errs {
		slog.Warn("parse error", "error", e)
		c.parseErrorsTotal.Inc()
	}

	// Build Prometheus metrics from extracted points.
	var metrics []prometheus.Metric
	for _, p := range points {
		desc, ok := c.descs[p.Name]
		if !ok {
			slog.Warn("unknown metric name from extractor", "name", p.Name)
			continue
		}

		m, err := prometheus.NewConstMetric(
			desc,
			typeToValueType(p.Type),
			p.Value,
			p.Labels["device"], // only variable label value
		)
		if err != nil {
			slog.Warn("create metric failed", "name", p.Name, "error", err)
			continue
		}

		metrics = append(metrics, m)
		ch <- m

		// Emit aliases for this metric.
		if aliases, ok := c.aliases[p.Name]; ok {
			for _, alias := range aliases {
				aliasDesc, ok := c.descs[alias]
				if !ok {
					continue
				}
				am, err := prometheus.NewConstMetric(
					aliasDesc,
					typeToValueType(p.Type),
					p.Value,
					p.Labels["device"],
				)
				if err != nil {
					slog.Warn("create alias metric failed", "alias", alias, "error", err)
					continue
				}
				metrics = append(metrics, am)
				ch <- am
			}
		}
	}

	c.cache = metrics
	c.upGauge.Set(1)
	c.lastScrape = time.Now()

	c.sendSelfMetrics(ch)
}

func (c *GPUCollector) sendSelfMetrics(ch chan<- prometheus.Metric) {
	c.upGauge.Collect(ch)
	c.scrapeDuration.Collect(ch)
	c.parseErrorsTotal.Collect(ch)
}

// typeToValueType converts model metric type to prometheus ValueType.
func typeToValueType(t string) prometheus.ValueType {
	switch t {
	case model.TypeCounter:
		return prometheus.CounterValue
	default:
		return prometheus.GaugeValue
	}
}
