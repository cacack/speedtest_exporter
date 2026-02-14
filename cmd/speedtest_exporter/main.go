package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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

func main() {
	port := flag.String("port", "9090", "listening port to expose metrics on")
	serverID := flag.Int("server_id", -1, "Speedtest.net server ID to run test against, -1 will pick the closest server to your location")
	serverFallback := flag.Bool("server_fallback", false, "If the server_id given is not available, should we fallback to closest available server")
	flag.Parse()

	exporter := exporter.New(*serverID, *serverFallback)

	r := prometheus.NewRegistry()
	r.MustRegister(exporter)

	http.HandleFunc("/", rootHandler())
	http.HandleFunc("/health", healthHandler())
	http.Handle(metricsPath, promhttp.HandlerFor(r, promhttp.HandlerOpts{
		MaxRequestsInFlight: 1,
		Timeout:             60 * time.Second,
	}))

	srv := &http.Server{
		Addr:         ":" + *port,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 70 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
