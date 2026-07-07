package extractor

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gpu-metrics-monitor/internal/config"
	"gpu-metrics-monitor/internal/model"
)

// regexExtractor applies a compiled regex to each line of smi stdout,
// extracting values via capture groups.
type regexExtractor struct {
	metricDef config.MetricDef
	rc        config.RegexConfig
	re        *regexp.Regexp
	unavail   map[string]bool
}

func newRegex(md config.MetricDef) (*regexExtractor, error) {
	re := md.CompiledRegex()
	if re == nil {
		return nil, fmt.Errorf("regex %q: no compiled pattern", md.Name)
	}

	unavail := make(map[string]bool, len(md.Regex.UnavailableValues))
	for _, v := range md.Regex.UnavailableValues {
		unavail[v] = true
	}

	return &regexExtractor{
		metricDef: md,
		rc:        *md.Regex,
		re:        re,
		unavail:   unavail,
	}, nil
}

func (r *regexExtractor) Extract(_ context.Context, stdout string) ([]model.MetricPoint, error) {
	var points []model.MetricPoint
	lines := strings.Split(stdout, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := r.re.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		// Safety: ensure capture groups exist.
		if len(matches) <= max(r.rc.DeviceGroup, r.rc.ValueGroup) {
			continue
		}

		rawValue := matches[r.rc.ValueGroup]
		if r.unavail[rawValue] {
			continue
		}

		val, err := parseFloatWithUnit(rawValue)
		if err != nil {
			continue
		}

		device := matches[r.rc.DeviceGroup]

		labels := make(map[string]string, len(r.metricDef.Labels)+1)
		labels["device"] = device
		for _, lbl := range r.metricDef.Labels {
			labels[lbl.Name] = lbl.Value
		}

		points = append(points, model.MetricPoint{
			Name:   r.metricDef.Name,
			Help:   r.metricDef.Help,
			Type:   r.metricDef.Type,
			Value:  val,
			Labels: labels,
		})
	}

	return points, nil
}
