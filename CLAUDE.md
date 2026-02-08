# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Prometheus exporter for Speedtest.net results, written in Go 1.25. Exports download/upload speed, latency, and server metadata as Prometheus metrics.

## Build & Development Commands

```bash
# Build
go build -o speedtest_exporter ./cmd/speedtest_exporter/main.go

# Test
go test ./...

# Lint (used in CI)
golangci-lint run

# Tidy modules
go mod tidy

# Build release snapshot (local multi-platform build)
goreleaser release --snapshot --clean

# Run locally
./speedtest_exporter -port 9090 -server_id -1 -server_fallback=false
```

## Architecture

Two-package structure:

- **`cmd/speedtest_exporter/main.go`** — HTTP server exposing `/metrics`, `/health`, and `/` endpoints. Registers the custom Prometheus collector. Limits concurrent scrapes to 1 with a 60s timeout since speedtests are slow and resource-intensive.

- **`internal/exporter/exporter.go`** — Implements `prometheus.Collector`. The `Collect()` method runs a full speedtest (ping, download, upload) via `speedtest-go` library and emits 5 gauges (`speedtest_up`, `speedtest_scrape_duration_seconds`, `speedtest_latency_seconds`, `speedtest_download_speed_Bps`, `speedtest_upload_speed_Bps`) with rich labels (user geo/ISP, server geo/id). The speedtest-go API returns bytes/sec directly.

## CI/CD

GitHub Actions workflows in `.github/workflows/`:
- **build.yaml** — Runs on push to main and PRs: lint + snapshot build
- **release.yaml** — Runs on `v*` tags: lint + goreleaser release to GitHub Releases and GHCR

Multi-arch Docker images (amd64, arm64, armv7) built on `gcr.io/distroless/static`.

## Key Dependencies

- `github.com/showwin/speedtest-go` — Speedtest.net client
- `github.com/prometheus/client_golang` — Prometheus metrics
- `github.com/google/uuid` — Test run UUIDs
- `log/slog` — Structured logging (stdlib)
