package config

import (
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level exporter configuration.
type Config struct {
	Command    string        `yaml:"command"`
	Timeout    time.Duration `yaml:"timeout"`
	Interval   time.Duration `yaml:"interval"`
	Listen     string        `yaml:"listen"`
	Auth       *AuthConfig   `yaml:"auth,omitempty"`
	AllowedIPs []string      `yaml:"allowed_ips,omitempty"`
	DeniedIPs  []string      `yaml:"denied_ips,omitempty"`
	Labels     []LabelDef    `yaml:"labels,omitempty"`
	Metrics    []MetricDef   `yaml:"metrics"`

	// Parsed IP lists (populated by Validate).
	allowedNets  []netip.Prefix
	allowedAddrs []netip.Addr
	deniedNets   []netip.Prefix
	deniedAddrs  []netip.Addr
}

// AuthConfig holds optional HTTP Basic Authentication credentials.
//
// Two password modes are supported:
//   - password: plaintext (development / low-security environments).
//   - password_hash: bcrypt hash, generated with -gen-htpasswd (production).
//
// password and password_hash are mutually exclusive.
type AuthConfig struct {
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	PasswordHash string `yaml:"password_hash"`
}

// MetricDef describes one metric to extract from smi output.
type MetricDef struct {
	Name     string         `yaml:"name"`
	Help     string         `yaml:"help"`
	Type     string         `yaml:"type"` // "gauge" or "counter"
	Kind     string         `yaml:"kind"` // "columnar", "regex", "js"
	Labels   []LabelDef     `yaml:"labels,omitempty"`
	Aliases  []string       `yaml:"aliases,omitempty"`
	Columnar *ColumnarConfig `yaml:"columnar,omitempty"`
	Regex    *RegexConfig    `yaml:"regex,omitempty"`
	JS       *JSConfig       `yaml:"js,omitempty"`

	compiledRegex *regexp.Regexp // cached compiled pattern for regex kind
}

// LabelDef is a static label applied to every data point of a metric.
type LabelDef struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// ColumnarConfig configures columnar (tabular) extraction.
type ColumnarConfig struct {
	ColumnName        string   `yaml:"column_name"`        // header text to match
	DeviceColumn      int      `yaml:"device_column"`      // zero-based column index for device ID
	SkipLines         int      `yaml:"skip_lines"`         // lines to skip before header
	UnavailableValues []string `yaml:"unavailable_values"` // values that mean "no data"
}

// RegexConfig configures regex-based extraction.
type RegexConfig struct {
	Pattern           string   `yaml:"pattern"`            // regex with capture groups
	DeviceGroup       int      `yaml:"device_group"`       // capture group index for device ID (1-based)
	ValueGroup        int      `yaml:"value_group"`        // capture group index for value (1-based)
	UnavailableValues []string `yaml:"unavailable_values"` // values that mean "no data"
}

// JSConfig configures JS-based extraction (goja sandbox).
type JSConfig struct {
	Script string `yaml:"script"` // JavaScript parsing code
}

// valid extract kinds
const (
	KindColumnar = "columnar"
	KindRegex    = "regex"
	KindJS       = "js"
)

// CompiledRegex returns the pre-compiled regex for this metric (regex kind only).
func (md *MetricDef) CompiledRegex() *regexp.Regexp {
	return md.compiledRegex
}

// LoadFile reads and validates a YAML config from a file path.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return LoadBytes(data)
}

// LoadBytes parses and validates YAML config from raw bytes.
func LoadBytes(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the config for semantic correctness and compiles regexes.
// It returns all errors found (not just the first), allowing the user to fix
// everything at once.
func (c *Config) Validate() error {
	var errs []string

	if c.Command == "" {
		errs = append(errs, "command is required")
	}
	if c.Timeout <= 0 {
		errs = append(errs, "timeout must be positive")
	}
	if c.Interval <= 0 {
		errs = append(errs, "interval must be positive")
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:9810"
	}
	if c.Listen != "" && !strings.Contains(c.Listen, ":") {
		errs = append(errs, "listen must be host:port format")
	}
	if c.Auth != nil {
		if c.Auth.Username == "" {
			errs = append(errs, "auth.username is required when auth is set")
		}
		if c.Auth.Password == "" && c.Auth.PasswordHash == "" {
			errs = append(errs, "auth.password or auth.password_hash is required when auth is set")
		}
		if c.Auth.Password != "" && c.Auth.PasswordHash != "" {
			errs = append(errs, "auth.password and auth.password_hash are mutually exclusive")
		}
	}

	// Validate and parse IP lists.
	c.allowedAddrs, c.allowedNets, errs = parseIPList(c.AllowedIPs, "allowed_ips", errs)
	c.deniedAddrs, c.deniedNets, errs = parseIPList(c.DeniedIPs, "denied_ips", errs)

	// Validate global labels.
	for i, lbl := range c.Labels {
		if lbl.Name == "" {
			errs = append(errs, fmt.Sprintf("labels[%d]: name is required", i))
		}
		if lbl.Name == "device" {
			errs = append(errs, fmt.Sprintf("labels[%d]: %q is reserved (used as the device identifier)", i, lbl.Name))
		}
	}

	if len(c.Metrics) == 0 {
		errs = append(errs, "at least one metric is required")
	}

	for i := range c.Metrics {
		m := &c.Metrics[i]
		if m.Name == "" {
			errs = append(errs, fmt.Sprintf("metrics[%d]: name is required", i))
		}
		if !isValidMetricName(m.Name) {
			errs = append(errs, fmt.Sprintf("metrics[%d]: name %q is not a valid metric name", i, m.Name))
		}
		if m.Help == "" {
			errs = append(errs, fmt.Sprintf("metrics[%d] (%s): help is required", i, m.Name))
		}
		if m.Type != "gauge" && m.Type != "counter" {
			errs = append(errs, fmt.Sprintf("metrics[%d] (%s): type must be 'gauge' or 'counter', got %q", i, m.Name, m.Type))
		}

		// Validate aliases (per-metric checks).
		for j, alias := range m.Aliases {
			if alias == "" {
				errs = append(errs, fmt.Sprintf("metrics[%d] (%s): aliases[%d] is empty", i, m.Name, j))
			} else if !isValidMetricName(alias) {
				errs = append(errs, fmt.Sprintf("metrics[%d] (%s): alias %q is not a valid metric name", i, m.Name, alias))
			} else if alias == m.Name {
				errs = append(errs, fmt.Sprintf("metrics[%d] (%s): alias %q must differ from the metric name", i, m.Name, alias))
			}
		}

		switch m.Kind {
		case KindColumnar:
			errs = append(errs, validateColumnar(i, m)...)
		case KindRegex:
			errs = append(errs, validateRegex(i, m)...)
		case KindJS:
			errs = append(errs, validateJS(i, m)...)
		default:
			errs = append(errs, fmt.Sprintf("metrics[%d] (%s): kind must be 'columnar', 'regex', or 'js', got %q", i, m.Name, m.Kind))
		}
	}

	// Check for duplicate names across all metrics (primary names and aliases).
	seen := make(map[string]int) // name -> metric index
	for i, m := range c.Metrics {
		if otherIdx, exists := seen[m.Name]; exists {
			errs = append(errs, fmt.Sprintf("duplicate metric name %q (metrics[%d] and metrics[%d])", m.Name, otherIdx, i))
		}
		seen[m.Name] = i
		for _, alias := range m.Aliases {
			if otherIdx, exists := seen[alias]; exists {
				if otherIdx == i {
					errs = append(errs, fmt.Sprintf("metrics[%d] (%s): duplicate alias %q", i, m.Name, alias))
				} else {
					errs = append(errs, fmt.Sprintf("alias %q in metrics[%d] (%s) conflicts with metrics[%d]", alias, i, m.Name, otherIdx))
				}
			}
			seen[alias] = i
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func validateColumnar(idx int, m *MetricDef) []string {
	var errs []string
	cc := m.Columnar
	if cc == nil {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): columnar config is required for kind=columnar", idx, m.Name))
		return errs
	}
	if cc.ColumnName == "" {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): columnar.column_name is required", idx, m.Name))
	}
	if cc.DeviceColumn < 0 {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): columnar.device_column must be >= 0", idx, m.Name))
	}
	return errs
}

func validateRegex(idx int, m *MetricDef) []string {
	var errs []string
	rc := m.Regex
	if rc == nil {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): regex config is required for kind=regex", idx, m.Name))
		return errs
	}
	if rc.Pattern == "" {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): regex.pattern is required", idx, m.Name))
	} else {
		re, err := regexp.Compile(rc.Pattern)
		if err != nil {
			errs = append(errs, fmt.Sprintf("metrics[%d] (%s): invalid regex pattern: %v", idx, m.Name, err))
		} else {
			m.compiledRegex = re
		}
	}
	if rc.ValueGroup < 1 {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): regex.value_group must be >= 1", idx, m.Name))
	}
	if rc.DeviceGroup < 1 {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): regex.device_group must be >= 1", idx, m.Name))
	}
	return errs
}

func validateJS(idx int, m *MetricDef) []string {
	var errs []string
	jc := m.JS
	if jc == nil {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): js config is required for kind=js", idx, m.Name))
		return errs
	}
	if strings.TrimSpace(jc.Script) == "" {
		errs = append(errs, fmt.Sprintf("metrics[%d] (%s): js.script is required", idx, m.Name))
	}
	return errs
}

// isValidMetricName checks that the name follows Prometheus naming conventions.
func isValidMetricName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for i, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || r == ':' {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// parseIPList validates a list of IP/CIDR strings and returns parsed results.
// Each entry may be a plain IP (e.g. "10.0.0.1") or a CIDR prefix (e.g. "10.0.0.0/24").
func parseIPList(entries []string, fieldName string, errs []string) ([]netip.Addr, []netip.Prefix, []string) {
	var addrs []netip.Addr
	var nets []netip.Prefix

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: invalid CIDR %q: %v", fieldName, entry, err))
				continue
			}
			nets = append(nets, prefix)
		} else {
			addr, err := netip.ParseAddr(entry)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: invalid IP %q: %v", fieldName, entry, err))
				continue
			}
			addrs = append(addrs, addr)
		}
	}
	return addrs, nets, errs
}

// IsIPAllowed checks whether the given client IP is permitted.
//
// Rules (in order):
//  1. If allowed_ips is configured, only IPs matching the list are permitted
//     (all others are denied, regardless of denied_ips).
//  2. If only denied_ips is configured, matching IPs are denied and all
//     others are permitted.
//  3. If neither is configured, all IPs are permitted.
func (c *Config) IsIPAllowed(addr netip.Addr) bool {
	// Allowlist mode: only listed IPs are permitted.
	if len(c.allowedAddrs) > 0 || len(c.allowedNets) > 0 {
		for _, a := range c.allowedAddrs {
			if addr == a {
				return true
			}
		}
		for _, n := range c.allowedNets {
			if n.Contains(addr) {
				return true
			}
		}
		return false
	}

	// Denylist mode: listed IPs are blocked.
	for _, a := range c.deniedAddrs {
		if addr == a {
			return false
		}
	}
	for _, n := range c.deniedNets {
		if n.Contains(addr) {
			return false
		}
	}

	return true
}
