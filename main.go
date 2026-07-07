package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"golang.org/x/crypto/bcrypt"

	"gpu-metrics-monitor/internal/collector"
	"gpu-metrics-monitor/internal/config"
	"gpu-metrics-monitor/internal/executor"
	"gpu-metrics-monitor/internal/extractor"
	"gpu-metrics-monitor/internal/model"
	"gpu-metrics-monitor/internal/server"
)

// version is set at build time via -ldflags "-X main.version=..." (see Makefile).
// Defaults to "dev" for untagged local builds.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: gpu-metrics-exporter <command> [options]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  run          Start the metrics exporter (default if no command given)")
		fmt.Fprintln(os.Stderr, "  validate     Validate configuration and exit (like nginx -t)")
		fmt.Fprintln(os.Stderr, "  test         Run one collection and output structured raw data (JSON)")
		fmt.Fprintln(os.Stderr, "  gen-htpasswd Generate bcrypt hash for basic auth")
		fmt.Fprintln(os.Stderr, "  version      Print build version and exit")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "validate":
		cmdValidate(os.Args[2:])
	case "gen-htpasswd":
		cmdGenHtpasswd(os.Args[2:])
	case "test":
		cmdTest(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "version":
		fmt.Printf("gpu-metrics-exporter %s\n", version)
	default:
		// Backward compatibility: if first arg looks like a flag, treat as "run".
		if strings.HasPrefix(os.Args[1], "-") {
			cmdRun(os.Args[1:])
		} else {
			fmt.Fprintf(os.Stderr, "Error: unknown command %q\n", os.Args[1])
			fmt.Fprintln(os.Stderr, "Available: run, validate, test, gen-htpasswd, version")
			os.Exit(1)
		}
	}
}

// cmdValidate loads and validates the configuration, then exits.
func cmdValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	fs.Parse(args)

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Error: -config <path> is required")
		os.Exit(1)
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config file %s: FAILED\n", *configPath)
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("config file %s: syntax OK\n", *configPath)
	fmt.Printf("  command:   %s\n", cfg.Command)
	fmt.Printf("  timeout:   %s\n", cfg.Timeout)
	fmt.Printf("  interval:  %s\n", cfg.Interval)
	fmt.Printf("  listen:    %s\n", cfg.Listen)
	fmt.Printf("  metrics:   %d\n", len(cfg.Metrics))
	for _, m := range cfg.Metrics {
		fmt.Printf("    - %s (%s)", m.Name, m.Type)
		if len(m.Aliases) > 0 {
			fmt.Printf(", aliases: %v", m.Aliases)
		}
		fmt.Println()
	}
}

// cmdTest runs one collection cycle and outputs structured raw metric points
// in a human-readable table (default) or JSON format, bypassing Prometheus/metrics.
func cmdTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	format := fs.String("format", "table", "Output format: table (human-readable) or json")
	showRaw := fs.Bool("raw", false, "Include raw command stdout in output")
	fs.Parse(args)

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Error: -config <path> is required")
		os.Exit(1)
	}
	if *format != "table" && *format != "json" {
		fmt.Fprintf(os.Stderr, "Error: --format must be 'table' or 'json', got %q\n", *format)
		os.Exit(1)
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	cmd := executor.New(cfg.Command, cfg.Timeout)

	pool, err := extractor.NewPool(cfg.Metrics)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating extractor pool: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	stdout, runErr := cmd.Run(ctx)

	points, parseErrs := pool.ExtractAll(ctx, stdout)

	if *format == "json" {
		printTestJSON(cfg.Command, runErr, stdout, points, parseErrs, *showRaw)
	} else {
		printTestTable(cfg.Command, runErr, stdout, points, parseErrs, *showRaw)
	}

	if runErr != nil {
		os.Exit(1)
	}
}

// printTestJSON outputs the test result as indented JSON.
func printTestJSON(command string, runErr error, rawOutput string,
	points []model.MetricPoint, parseErrs []error, showRaw bool) {

	output := struct {
		Command     string              `json:"command"`
		Success     bool                `json:"success"`
		Error       string              `json:"error,omitempty"`
		RawOutput   string              `json:"raw_output,omitempty"`
		Metrics     []model.MetricPoint `json:"metrics"`
		ParseErrors []string            `json:"parse_errors,omitempty"`
	}{
		Command: command,
		Success: runErr == nil,
		Metrics: points,
	}
	if runErr != nil {
		output.Error = runErr.Error()
	}
	if showRaw && rawOutput != "" {
		output.RawOutput = rawOutput
	}
	for _, e := range parseErrs {
		output.ParseErrors = append(output.ParseErrors, e.Error())
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}
}

// printTestTable outputs the test result in a human-readable tabular format.
func printTestTable(command string, runErr error, rawOutput string,
	points []model.MetricPoint, parseErrs []error, showRaw bool) {

	// Header block.
	fmt.Println("══════════════════════════════════════════════════════════")
	fmt.Println("  GPU Metrics Test")
	fmt.Println("══════════════════════════════════════════════════════════")
	fmt.Printf("  Command:  %s\n", command)
	if runErr != nil {
		fmt.Printf("  Status:   ❌ FAILED — %v\n", runErr)
	} else {
		fmt.Printf("  Status:   ✅ OK\n")
	}
	fmt.Printf("  Metrics:  %d collected\n", len(points))
	fmt.Println()

	// Group points by device for readability.
	if len(points) > 0 {
		byDevice := groupByDevice(points)
		devices := sortedKeys(byDevice)

		for _, device := range devices {
			fmt.Printf("── Device %s ──\n", device)

			w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  Metric\tValue\tType\tHelp")
			fmt.Fprintln(w, "  ──────\t─────\t────\t────")

			for _, p := range byDevice[device] {
				fmt.Fprintf(w, "  %s\t%.4f\t%s\t%s\n",
					p.Name, p.Value, p.Type, p.Help)
			}
			w.Flush()
			fmt.Println()
		}
	} else {
		fmt.Println("  (no metrics extracted)")
		fmt.Println()
	}

	// Parse errors.
	if len(parseErrs) > 0 {
		fmt.Println("── Parse Errors ──")
		for _, e := range parseErrs {
			fmt.Printf("  ⚠  %v\n", e)
		}
		fmt.Println()
	}

	// Raw output: shown when --raw is set, or automatically when there are
	// parse errors (to help diagnose skip_lines / column config issues).
	if (showRaw || len(parseErrs) > 0) && rawOutput != "" {
		label := "── Raw Command Output ──"
		if !showRaw && len(parseErrs) > 0 {
			label = "── Raw Command Output (shown because of parse errors) ──"
		}
		fmt.Println(label)
		fmt.Printf("  $ sh -c %q\n", command)
		for i, line := range strings.Split(rawOutput, "\n") {
			fmt.Printf("  %3d: %s\n", i+1, line)
		}
		fmt.Println()
	}
}

// groupByDevice partitions metric points by their "device" label.
func groupByDevice(points []model.MetricPoint) map[string][]model.MetricPoint {
	m := make(map[string][]model.MetricPoint)
	for _, p := range points {
		dev := p.Labels["device"]
		m[dev] = append(m[dev], p)
	}
	return m
}

// sortedKeys returns the sorted keys of a string-keyed map.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cmdGenHtpasswd generates a bcrypt hash for the given username.
func cmdGenHtpasswd(args []string) {
	fs := flag.NewFlagSet("gen-htpasswd", flag.ExitOnError)
	fs.Parse(args)

	username := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if username == "" {
		fmt.Fprintln(os.Stderr, "Usage: gpu-metrics-exporter gen-htpasswd <username>")
		fmt.Fprintln(os.Stderr, "Error: username is required")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Password for %s: ", username)
	reader := bufio.NewReader(os.Stdin)
	password, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading password: %v\n", err)
		os.Exit(1)
	}
	password = strings.TrimRight(password, "\r\n")

	if password == "" {
		fmt.Fprintln(os.Stderr, "Error: password must not be empty")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating hash: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("auth:\n  username: %q\n  password_hash: %q\n", username, string(hash))
}

// cmdRun starts the metrics exporter server.
func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	listen := fs.String("listen", "", "Override HTTP listen address (host:port)")
	fs.Parse(args)

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Error: -config <path> is required")
		os.Exit(1)
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if *listen != "" {
		cfg.Listen = *listen
	}

	slog.Info("gpu-metrics-exporter starting",
		"version", version,
		"command", cfg.Command,
		"listen", cfg.Listen,
		"interval", cfg.Interval.String(),
		"metrics", len(cfg.Metrics),
	)

	col, err := collector.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating collector: %v\n", err)
		os.Exit(1)
	}

	srv := server.New(cfg, col)
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
