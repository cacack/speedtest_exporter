package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cacack/speedtest_exporter/internal/exporter"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	metricsPath = "/metrics"
)

func rootHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>
             <head><title>Speedtest Exporter</title></head>
             <body>
             <h1>Speedtest Exporter</h1>
             <p>Metrics page will take approx 40 seconds to load and show results, as the exporter carries out a speedtest when scraped.</p>
             <p><a href='` + metricsPath + `'>Metrics</a></p>
             <p><a href='/health'>Health</a></p>
             </body>
             </html>`))
	}
}

func healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	}
}

// contextCollector bridges prometheus.Collector with context-aware collection.
type contextCollector struct {
	e   *exporter.Exporter
	ctx context.Context
}

func (c *contextCollector) Describe(ch chan<- *prometheus.Desc) { c.e.Describe(ch) }
func (c *contextCollector) Collect(ch chan<- prometheus.Metric) { c.e.CollectWithContext(c.ctx, ch) }

// metricsHandler returns an HTTP handler that passes request context to the exporter.
func metricsHandler(e *exporter.Exporter) http.Handler {
	// Use a TryLock to limit to 1 concurrent scrape (replaces promhttp MaxRequestsInFlight).
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mu.TryLock() {
			http.Error(w, "Scrape already in progress", http.StatusServiceUnavailable)
			return
		}
		defer mu.Unlock()

		reg := prometheus.NewRegistry()
		reg.MustRegister(&contextCollector{e: e, ctx: r.Context()})
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})
}

func main() {
	port := flag.String("port", "9090", "listening port to expose metrics on")
	serverID := flag.Int("server_id", -1, "Speedtest.net server ID to run test against, -1 will pick the closest server to your location")
	serverFallback := flag.Bool("server_fallback", false, "If the server_id given is not available, should we fallback to closest available server")
	flag.Parse()

	exp := exporter.New(*serverID, *serverFallback)

	http.HandleFunc("/", rootHandler())
	http.HandleFunc("/health", healthHandler())
	http.Handle(metricsPath, metricsHandler(exp))

	srv := &http.Server{
		Addr:         ":" + *port,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 70 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Create context that cancels on SIGTERM/SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Start server in goroutine.
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("server started", "port", *port)

	// Wait for shutdown signal.
	<-ctx.Done()
	slog.Info("shutting down server")

	// Give in-flight requests time to complete.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
}
