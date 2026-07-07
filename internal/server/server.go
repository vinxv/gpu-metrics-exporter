package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/bcrypt"

	"gpu-metrics-monitor/internal/collector"
	"gpu-metrics-monitor/internal/config"
)

// Server wraps the HTTP server with auth, IP filtering, and graceful shutdown.
type Server struct {
	addr      string
	cfg       *config.Config
	auth      *config.AuthConfig
	collector *collector.GPUCollector
	http      *http.Server
}

// New creates a new Server, registers the collector, and sets up routes.
func New(cfg *config.Config, col *collector.GPUCollector) *Server {
	registry := prometheus.NewRegistry()
	registry.MustRegister(col)

	s := &Server{
		addr:      cfg.Listen,
		cfg:       cfg,
		auth:      cfg.Auth,
		collector: col,
	}

	mux := http.NewServeMux()

	// /metrics endpoint with IP filtering and optional basic auth.
	metricsHandler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	mux.Handle("/metrics", s.ipFilterMiddleware(s.authMiddleware(metricsHandler)))

	// /healthz endpoint (IP filtering + basic auth if configured, otherwise open).
	mux.HandleFunc("/healthz", s.ipFilterMiddlewareFunc(s.authMiddlewareFunc(s.healthzHandler)))

	s.http = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s
}

// Start begins listening and blocks until a shutdown signal is received.
func (s *Server) Start() error {
	errCh := make(chan error, 1)

	go func() {
		slog.Info("starting HTTP server", "addr", s.addr)
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("listen: %w", err)
		}
	}()

	// Wait for signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig.String())
	case err := <-errCh:
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	slog.Info("server stopped")
	return nil
}

// authMiddleware wraps an http.Handler with optional basic auth.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if s.auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAuth(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="GPU Metrics Exporter"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authMiddlewareFunc wraps a handler function with optional basic auth.
func (s *Server) authMiddlewareFunc(next http.HandlerFunc) http.HandlerFunc {
	if s.auth == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAuth(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="GPU Metrics Exporter"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ipFilterMiddleware wraps an http.Handler and rejects requests from
// IPs not permitted by the allowlist/denylist configuration.
func (s *Server) ipFilterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.checkIP(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ipFilterMiddlewareFunc is the HandlerFunc variant of ipFilterMiddleware.
func (s *Server) ipFilterMiddlewareFunc(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkIP(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// checkIP extracts the client IP from the request and validates it
// against the configured IP allowlist/denylist.
func (s *Server) checkIP(r *http.Request) bool {
	addrPort, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		slog.Warn("failed to parse remote address", "remote_addr", r.RemoteAddr, "error", err)
		return false
	}
	return s.cfg.IsIPAllowed(addrPort.Addr())
}

func (s *Server) checkAuth(r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}

	const prefix = "Basic "
	if !strings.HasPrefix(authHeader, prefix) {
		return false
	}

	decoded, err := base64.StdEncoding.DecodeString(authHeader[len(prefix):])
	if err != nil {
		return false
	}

	creds := strings.SplitN(string(decoded), ":", 2)
	if len(creds) != 2 {
		return false
	}

	usernameOk := subtle.ConstantTimeCompare([]byte(creds[0]), []byte(s.auth.Username)) == 1

	var passwordOk bool
	if s.auth.PasswordHash != "" {
		passwordOk = bcrypt.CompareHashAndPassword([]byte(s.auth.PasswordHash), []byte(creds[1])) == nil
	} else {
		passwordOk = subtle.ConstantTimeCompare([]byte(creds[1]), []byte(s.auth.Password)) == 1
	}

	return usernameOk && passwordOk
}

func (s *Server) healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
