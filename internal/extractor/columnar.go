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
	unavail   map[string]bool
}

func newColumnar(md config.MetricDef) (*columnarExtractor, error) {
	unavail := make(map[string]bool, len(md.Columnar.UnavailableValues))
	for _, v := range md.Columnar.UnavailableValues {
		unavail[v] = true
	}
	return &columnarExtractor{
		metricDef: md,
		cc:        *md.Columnar,
		unavail:   unavail,
	}, nil
}

func (c *columnarExtractor) Extract(_ context.Context, stdout string) ([]model.MetricPoint, error) {
	table, err := parseColumnarTable(strings.Split(stdout, "\n"), c.cc.SkipLines)
	return c.extractTable(table, err)
}

type columnarTable struct {
	headers     []string
	headerLine  int
	wordColumns map[string]int
	colStarts   []int
	rows        [][]string
}

func parseColumnarTable(lines []string, skipLines int) (columnarTable, error) {
	headers, dataStart, err := scanHeaderZone(lines, skipLines)
	if err != nil {
		return columnarTable{}, err
	}

	table := columnarTable{
		headers:     headers,
		headerLine:  dataStart,
		colStarts:   columnStartPositions(lines[dataStart]),
		wordColumns: make(map[string]int),
	}
	for _, hdr := range headers {
		starts := columnStartPositions(hdr)
		for col, start := range starts[:min(len(starts), len(table.colStarts))] {
			end := len(hdr)
			if col+1 < len(starts) {
				end = starts[col+1]
			}
			word := strings.TrimSpace(hdr[start:end])
			if _, exists := table.wordColumns[word]; !exists {
				table.wordColumns[word] = col
			}
		}
	}
	for _, line := range lines[dataStart:] {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			table.rows = append(table.rows, strings.Fields(line))
		}
	}
	return table, nil
}

func (c *columnarExtractor) extractTable(table columnarTable, err error) ([]model.MetricPoint, error) {
	if err != nil {
		return nil, fmt.Errorf("columnar %q: %w", c.metricDef.Name, err)
	}

	valueCol := table.findColumn(c.cc.ColumnName)
	if valueCol < 0 {
		return nil, fmt.Errorf("columnar %q: column %q not found in headers %q (skip_lines=%d, header_line=%d)",
			c.metricDef.Name, c.cc.ColumnName, table.headers, c.cc.SkipLines, table.headerLine)
	}

	lastCol := max(c.cc.DeviceColumn, valueCol)
	for _, col := range c.cc.LabelColumns {
		lastCol = max(lastCol, col)
	}
	var points []model.MetricPoint
	for _, tokens := range table.rows {
		if len(tokens) <= lastCol {
			continue
		}

		rawValue := tokens[valueCol]
		if c.unavail[rawValue] {
			continue
		}

		val, err := parseFloatWithUnit(rawValue)
		if err != nil {
			continue
		}

		labels := make(map[string]string, len(c.metricDef.Labels)+len(c.cc.LabelColumns)+1)
		labels["device"] = tokens[c.cc.DeviceColumn]
		for _, lbl := range c.metricDef.Labels {
			labels[lbl.Name] = lbl.Value
		}
		for name, col := range c.cc.LabelColumns {
			labels[name] = tokens[col]
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

// findColumn searches the header for targetName and returns its column index.
func (t columnarTable) findColumn(targetName string) int {
	if col, ok := t.wordColumns[targetName]; ok {
		return col
	}

	dataColStarts := t.colStarts
	// Strategy 2: data-aligned midpoint — for multi-word column names where
	// the column name spans multiple header word-starts but maps to one data column.
	for _, hdr := range t.headers {
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
	for _, hdr := range t.headers {
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
