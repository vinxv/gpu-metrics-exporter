package collector

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
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
	descs   map[string]metricDescriptor
	aliases map[string][]string // primary name -> alias names

	// Self-monitoring metrics.
	upGauge          prometheus.Gauge
	scrapeDuration   prometheus.Gauge
	parseErrorsTotal prometheus.Counter
}

type metricDescriptor struct {
	desc        *prometheus.Desc
	labelNames  []string
	constLabels prometheus.Labels
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
		descs:   make(map[string]metricDescriptor),
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

	// Per-metric static labels override global labels.
	for _, md := range cfg.Metrics {
		constLabels := prometheus.Labels{}
		for _, lbl := range cfg.Labels {
			constLabels[lbl.Name] = lbl.Value
		}
		for _, lbl := range md.Labels {
			constLabels[lbl.Name] = lbl.Value
		}
		labelNames := md.VariableLabels()
		for _, name := range append([]string{md.Name}, md.Aliases...) {
			c.descs[name] = metricDescriptor{
				desc:        prometheus.NewDesc(name, md.Help, labelNames, constLabels),
				labelNames:  labelNames,
				constLabels: constLabels,
			}
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
		ch <- desc.desc
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
	cacheHit := c.collectFromCache(ch)
	c.mu.RUnlock()
	if cacheHit {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-checked locking.
	if c.collectFromCache(ch) {
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

		labelValues, err := desc.labelValues(p)
		if err != nil {
			slog.Warn("parse error", "error", err)
			c.parseErrorsTotal.Inc()
			continue
		}

		valueType := typeToValueType(p.Type)
		m, err := prometheus.NewConstMetric(desc.desc, valueType, p.Value, labelValues...)
		if err != nil {
			slog.Warn("create metric failed", "name", p.Name, "error", err)
			continue
		}

		metrics = append(metrics, m)
		ch <- m

		// Emit aliases for this metric.
		for _, alias := range c.aliases[p.Name] {
			am, err := prometheus.NewConstMetric(c.descs[alias].desc, valueType, p.Value, labelValues...)
			if err != nil {
				slog.Warn("create alias metric failed", "alias", alias, "error", err)
				continue
			}
			metrics = append(metrics, am)
			ch <- am
		}
	}

	c.cache = metrics
	c.upGauge.Set(1)
	c.lastScrape = time.Now()

	c.sendSelfMetrics(ch)
}

func (d metricDescriptor) labelValues(p model.MetricPoint) ([]string, error) {
	values := make([]string, len(d.labelNames))
	for i, name := range d.labelNames {
		value, exists := p.Labels[name]
		if !exists && name != "device" {
			return nil, fmt.Errorf("metric %q: missing variable label %q", p.Name, name)
		}
		values[i] = value
	}
	for name := range p.Labels {
		if _, static := d.constLabels[name]; !static && !slices.Contains(d.labelNames, name) {
			slog.Warn("ignoring undeclared metric label", "metric", p.Name, "label", name)
		}
	}
	return values, nil
}

// collectFromCache requires c.mu to be held.
func (c *GPUCollector) collectFromCache(ch chan<- prometheus.Metric) bool {
	if time.Since(c.lastScrape) >= c.cfg.Interval || len(c.cache) == 0 {
		return false
	}
	for _, m := range c.cache {
		ch <- m
	}
	c.sendSelfMetrics(ch)
	return true
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
