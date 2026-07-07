package extractor

import (
	"strconv"
	"strings"
)

// parseFloatWithUnit strips trailing non-numeric, non-dot characters
// (e.g. unit suffixes like C, W, %) before parsing as float64.
func parseFloatWithUnit(s string) (float64, error) {
	cleaned := strings.TrimRightFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.'
	})
	return strconv.ParseFloat(cleaned, 64)
}
