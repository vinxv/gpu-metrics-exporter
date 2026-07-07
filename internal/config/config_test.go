package config

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestLoadBytesValidColumnar(t *testing.T) {
	data := []byte(`
command: "echo hello"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: test_metric
    help: "A test metric"
    type: gauge
    kind: columnar
    columnar:
      column_name: "Value"
      device_column: 0
      unavailable_values: ["-"]
`)
	cfg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Command != "echo hello" {
		t.Errorf("command = %q, want %q", cfg.Command, "echo hello")
	}
	if cfg.Timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", cfg.Timeout)
	}
	if len(cfg.Metrics) != 1 {
		t.Fatalf("metrics count = %d, want 1", len(cfg.Metrics))
	}
	m := cfg.Metrics[0]
	if m.Name != "test_metric" {
		t.Errorf("metric name = %q", m.Name)
	}
	if m.Columnar.ColumnName != "Value" {
		t.Errorf("columnar.column_name = %q", m.Columnar.ColumnName)
	}
}

func TestLoadBytesValidRegex(t *testing.T) {
	data := []byte(`
command: "echo hello"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: test_metric
    help: "A test metric"
    type: gauge
    kind: regex
    regex:
      pattern: 'Device (\d+): (\d+)%'
      device_group: 1
      value_group: 2
      unavailable_values: ["-"]
`)
	cfg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := cfg.Metrics[0]
	if m.compiledRegex == nil {
		t.Error("regex should be compiled")
	}
	if m.Regex.DeviceGroup != 1 {
		t.Errorf("device_group = %d", m.Regex.DeviceGroup)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "invalid metric type",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m
    help: h
    type: histogram
    kind: columnar
    columnar:
      column_name: V
      device_column: 0
`,
			want: "type must be 'gauge' or 'counter'",
		},
		{
			name: "invalid metric name",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: 123bad
    help: h
    type: gauge
    kind: columnar
    columnar:
      column_name: V
      device_column: 0
`,
			want: "not a valid metric name",
		},
		{
			name: "invalid regex pattern",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m
    help: h
    type: gauge
    kind: regex
    regex:
      pattern: '[invalid'
      device_group: 1
      value_group: 1
`,
			want: "invalid regex pattern",
		},
		{
			name: "missing columnar config",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
`,
			want: "columnar config is required",
		},
		{
			name: "missing js script",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m
    help: h
    type: gauge
    kind: js
    js:
      script: ""
`,
			want: "js.script is required",
		},
		{
			name: "no metrics",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics: []
`,
			want: "at least one metric",
		},
		{
			name: "invalid alias name",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    aliases:
      - "123bad"
    columnar:
      column_name: V
      device_column: 0
`,
			want: "not a valid metric name",
		},
		{
			name: "alias equals metric name",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    aliases:
      - "m"
    columnar:
      column_name: V
      device_column: 0
`,
			want: "must differ",
		},
		{
			name: "empty alias",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    aliases:
      - ""
    columnar:
      column_name: V
      device_column: 0
`,
			want: "is empty",
		},
		{
			name: "alias conflicts with another metric name",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m1
    help: h
    type: gauge
    kind: columnar
    columnar:
      column_name: V
      device_column: 0
  - name: m2
    help: h
    type: gauge
    kind: columnar
    aliases:
      - "m1"
    columnar:
      column_name: V
      device_column: 0
`,
			want: "conflicts",
		},
		{
			name: "duplicate aliases in same metric",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    aliases:
      - "dup"
      - "dup"
    columnar:
      column_name: V
      device_column: 0
`,
			want: "duplicate alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestLoadBytesValidAliases(t *testing.T) {
	data := []byte(`
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
metrics:
  - name: brand_specific_metric
    help: "Brand-specific metric"
    type: gauge
    kind: columnar
    aliases:
      - device_generic_metric
    columnar:
      column_name: "Value"
      device_column: 0
`)
	cfg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Metrics) != 1 {
		t.Fatalf("metrics count = %d, want 1", len(cfg.Metrics))
	}
	if len(cfg.Metrics[0].Aliases) != 1 {
		t.Fatalf("aliases count = %d, want 1", len(cfg.Metrics[0].Aliases))
	}
	if cfg.Metrics[0].Aliases[0] != "device_generic_metric" {
		t.Errorf("alias = %q, want %q", cfg.Metrics[0].Aliases[0], "device_generic_metric")
	}
}

func TestAllowedIPsConfig(t *testing.T) {
	data := []byte(`
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
allowed_ips:
  - "127.0.0.1"
  - "10.0.0.0/8"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    columnar:
      column_name: V
      device_column: 0
`)
	cfg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.allowedAddrs) != 1 {
		t.Errorf("allowedAddrs count = %d, want 1", len(cfg.allowedAddrs))
	}
	if len(cfg.allowedNets) != 1 {
		t.Errorf("allowedNets count = %d, want 1", len(cfg.allowedNets))
	}
}

func TestDeniedIPsConfig(t *testing.T) {
	data := []byte(`
command: "echo"
timeout: 5s
interval: 10s
listen: "127.0.0.1:9999"
denied_ips:
  - "192.168.1.100"
  - "172.16.0.0/12"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    columnar:
      column_name: V
      device_column: 0
`)
	cfg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.deniedAddrs) != 1 {
		t.Errorf("deniedAddrs count = %d, want 1", len(cfg.deniedAddrs))
	}
	if len(cfg.deniedNets) != 1 {
		t.Errorf("deniedNets count = %d, want 1", len(cfg.deniedNets))
	}
}

func TestIPFilterValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "invalid CIDR in allowed_ips",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
allowed_ips:
  - "not-an-ip"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    columnar:
      column_name: V
      device_column: 0
`,
			want: "invalid IP",
		},
		{
			name: "invalid CIDR prefix",
			yaml: `
command: "echo"
timeout: 5s
interval: 10s
denied_ips:
  - "10.0.0.0/99"
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    columnar:
      column_name: V
      device_column: 0
`,
			want: "invalid CIDR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestIsIPAllowed(t *testing.T) {
	// Build a config with parsed IP lists directly.
	cfg := &Config{}
	cfg.allowedAddrs, cfg.allowedNets, _ = parseIPList([]string{"127.0.0.1", "10.0.0.0/8"}, "", nil)

	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"exact match", "127.0.0.1", true},
		{"in subnet", "10.1.2.3", true},
		{"subnet boundary", "10.255.255.255", true},
		{"not in list", "192.168.1.1", false},
		{"localhost IPv6 not in list", "::1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.ip)
			got := cfg.IsIPAllowed(addr)
			if got != tt.want {
				t.Errorf("IsIPAllowed(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsIPDenied(t *testing.T) {
	cfg := &Config{}
	cfg.deniedAddrs, cfg.deniedNets, _ = parseIPList([]string{"192.168.1.100", "172.16.0.0/12"}, "", nil)

	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"exact deny match", "192.168.1.100", false},
		{"in denied subnet", "172.16.5.5", false},
		{"not in deny list", "10.0.0.1", true},
		{"localhost not denied", "127.0.0.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.ip)
			got := cfg.IsIPAllowed(addr)
			if got != tt.want {
				t.Errorf("IsIPAllowed(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsIPAllowedAllowlistTakesPrecedence(t *testing.T) {
	// When both lists are configured, allowlist takes precedence.
	cfg := &Config{}
	cfg.allowedAddrs, cfg.allowedNets, _ = parseIPList([]string{"10.0.0.0/8"}, "", nil)
	cfg.deniedAddrs, cfg.deniedNets, _ = parseIPList([]string{"10.0.0.1"}, "", nil)

	addr := netip.MustParseAddr("10.0.0.1")
	if !cfg.IsIPAllowed(addr) {
		t.Error("allowlist should take precedence over denylist")
	}
}

func TestDefaultListen(t *testing.T) {
	data := []byte(`
command: "echo"
timeout: 5s
interval: 10s
metrics:
  - name: m
    help: h
    type: gauge
    kind: columnar
    columnar:
      column_name: V
      device_column: 0
`)
	cfg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9810" {
		t.Errorf("default listen = %q, want 127.0.0.1:9810", cfg.Listen)
	}
}
