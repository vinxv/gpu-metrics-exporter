package extractor

import (
	"context"
	"fmt"
	"strings"

	"gpu-metrics-monitor/internal/config"
	"gpu-metrics-monitor/internal/model"
)

// columnarExtractor parses tabular smi output where columns are named
// in a header row and data rows follow.
type columnarExtractor struct {
	metricDef config.MetricDef
	cc        config.ColumnarConfig
}

func newColumnar(md config.MetricDef) (*columnarExtractor, error) {
	return &columnarExtractor{
		metricDef: md,
		cc:        *md.Columnar,
	}, nil
}

func (c *columnarExtractor) Extract(_ context.Context, stdout string) ([]model.MetricPoint, error) {
	lines := strings.Split(stdout, "\n")

	// Build index of unavailable values for fast lookup.
	unavail := make(map[string]bool, len(c.cc.UnavailableValues))
	for _, v := range c.cc.UnavailableValues {
		unavail[v] = true
	}

	// Phase 1: Find header zone and the first data line.
	headerTexts, dataStart, err := scanHeaderZone(lines, c.cc.SkipLines)
	if err != nil {
		return nil, fmt.Errorf("columnar %q: %w", c.metricDef.Name, err)
	}
	if dataStart >= len(lines) {
		return nil, fmt.Errorf("columnar %q: no data lines after header", c.metricDef.Name)
	}

	// Phase 2: Determine column boundaries from the first data line.
	// Column boundaries are character-position based and apply to both
	// header and data lines. This handles multi-word column names correctly.
	colStarts := columnStartPositions(lines[dataStart])

	// Phase 3: Find the target column name in the header text.
	// Search across all header lines (for illuvatar-style multi-line headers).
	valueCol := findColumnInHeaders(headerTexts, colStarts, c.cc.ColumnName)
	if valueCol < 0 {
		// dataStart points to the first data line; the header is the line before it.
		headerLine := dataStart // 1-based line number in the output
		return nil, fmt.Errorf("columnar %q: column %q not found in headers %q (skip_lines=%d, header_line=%d)",
			c.metricDef.Name, c.cc.ColumnName, headerTexts, c.cc.SkipLines, headerLine)
	}

	// Phase 4: Parse all data lines using the column index.
	var points []model.MetricPoint
	for i := dataStart; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		tokens := strings.Fields(line)
		if len(tokens) <= max(c.cc.DeviceColumn, valueCol) {
			continue
		}

		rawValue := tokens[valueCol]
		if unavail[rawValue] {
			continue
		}

		val, err := parseFloatWithUnit(rawValue)
		if err != nil {
			continue
		}

		device := tokens[c.cc.DeviceColumn]

		labels := make(map[string]string, len(c.metricDef.Labels)+1)
		labels["device"] = device
		for _, lbl := range c.metricDef.Labels {
			labels[lbl.Name] = lbl.Value
		}

		points = append(points, model.MetricPoint{
			Name:   c.metricDef.Name,
			Help:   c.metricDef.Help,
			Type:   c.metricDef.Type,
			Value:  val,
			Labels: labels,
		})
	}

	return points, nil
}

// scanHeaderZone finds the header zone (comment-prefixed lines or first
// non-empty non-comment line) and returns the header text lines and the
// index of the first data line.
//
// Header format A (ascend-style): first non-empty line is the header.
// Header format B (illuvatar-style): consecutive #-prefixed lines are headers.
func scanHeaderZone(lines []string, skipLines int) (headers []string, dataStart int, err error) {
	headerEnd := skipLines - 1
	seenNonComment := false

	for i := skipLines; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			stripped := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if stripped != "" {
				headers = append(headers, stripped)
				headerEnd = i
			}
			continue
		}

		// Non-comment, non-empty line.
		if len(headers) == 0 && !seenNonComment {
			// No comment headers seen — this line is the header.
			headers = append(headers, line)
			headerEnd = i
			seenNonComment = true
			continue
		}

		// We've found headers already. This is the first data line.
		return headers, i, nil
	}

	if len(headers) == 0 {
		return nil, -1, fmt.Errorf("no header found")
	}
	return headers, headerEnd + 1, fmt.Errorf("no data lines after header (headerEnd=%d, totalLines=%d)", headerEnd, len(lines))
}

// columnStartPositions returns the character position of each whitespace-delimited
// field in the line. This defines the column structure that applies to both
// header and data.
func columnStartPositions(dataLine string) []int {
	var starts []int
	inField := false
	for i, ch := range dataLine {
		if ch == ' ' || ch == '\t' {
			inField = false
		} else if !inField {
			starts = append(starts, i)
			inField = true
		}
	}
	return starts
}

// findColumnInHeaders searches header text lines for targetName and returns
// the column index.
//
// Two strategies are tried in order:
//  1. Header word-starts: works for single-word column names (illuvatar).
//     Each word-start in the header defines a column region.
//  2. Data-aligned midpoint: works for multi-word column names (ascend).
//     The column name's midpoint in the header is mapped to a data column region.
func findColumnInHeaders(headers []string, dataColStarts []int, targetName string) int {
	// Strategy 1: header word-starts — each word-start is a column boundary.
	// Works for single-word column names (illuvatar). Must validate that the
	// column index is within the data column count, because multi-word header
	// names like "AI Core(%)" can inflate the header word count.
	for _, hdr := range headers {
		hdrStarts := columnStartPositions(hdr)
		nCols := len(hdrStarts)
		for col := 0; col < nCols; col++ {
			start := hdrStarts[col]
			end := len(hdr)
			if col+1 < nCols {
				end = hdrStarts[col+1]
			}
			region := strings.TrimSpace(hdr[start:end])
			if region == targetName && col < len(dataColStarts) {
				return col
			}
		}
	}

	// Strategy 2: data-aligned midpoint — for multi-word column names where
	// the column name spans multiple header word-starts but maps to one data column.
	for _, hdr := range headers {
		pos := strings.Index(hdr, targetName)
		if pos < 0 {
			continue
		}
		mid := pos + len(targetName)/2
		nCols := len(dataColStarts)
		for col := 0; col < nCols; col++ {
			start := dataColStarts[col]
			end := len(hdr)
			if col+1 < nCols {
				end = dataColStarts[col+1]
			}
			if mid >= start && mid < end {
				return col
			}
		}
		if mid >= dataColStarts[nCols-1] {
			return nCols - 1
		}
	}

	// Strategy 3: substring match within data column regions (loose fallback).
	for _, hdr := range headers {
		nCols := len(dataColStarts)
		for col := 0; col < nCols; col++ {
			start := dataColStarts[col]
			end := len(hdr)
			if col+1 < nCols {
				end = dataColStarts[col+1]
			}
			if start >= len(hdr) {
				continue
			}
			region := strings.TrimSpace(hdr[start:end])
			if strings.Contains(region, targetName) {
				return col
			}
		}
	}

	return -1
}
