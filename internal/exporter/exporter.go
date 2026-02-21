package exporter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/showwin/speedtest-go/speedtest"
)

const (
	namespace = "speedtest"
)

var (
	up = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "up"),
		"Whether the last speedtest was successful",
		nil, nil,
	)
	scrapeDurationSeconds = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "scrape_duration_seconds"),
		"Duration of the last speedtest scrape in seconds",
		nil, nil,
	)
	latency = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "latency_seconds"),
		"Measured latency in seconds from the last speedtest",
		[]string{"user_lat", "user_lon", "user_ip", "user_isp", "server_lat", "server_lon", "server_id", "server_name", "server_country", "distance"},
		nil,
	)
	upload = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "upload_speed_bytes_per_second"),
		"Upload speed in bytes per second from the last speedtest",
		[]string{"user_lat", "user_lon", "user_ip", "user_isp", "server_lat", "server_lon", "server_id", "server_name", "server_country", "distance"},
		nil,
	)
	download = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "download_speed_bytes_per_second"),
		"Download speed in bytes per second from the last speedtest",
		[]string{"user_lat", "user_lon", "user_ip", "user_isp", "server_lat", "server_lon", "server_id", "server_name", "server_country", "distance"},
		nil,
	)
)

// SpeedtestClient abstracts the speedtest-go client.
type SpeedtestClient interface {
	FetchUserInfo(ctx context.Context) (*speedtest.User, error)
	FetchServers(ctx context.Context) (speedtest.Servers, error)
}

// ServerRunner abstracts speed test execution on a server.
type ServerRunner interface {
	PingTest(ctx context.Context, server *speedtest.Server) error
	DownloadTest(ctx context.Context, server *speedtest.Server) error
	UploadTest(ctx context.Context, server *speedtest.Server) error
}

// defaultRunner calls the real speedtest server methods.
type defaultRunner struct{}

func (d *defaultRunner) PingTest(ctx context.Context, server *speedtest.Server) error {
	return server.PingTestContext(ctx, nil)
}

func (d *defaultRunner) DownloadTest(ctx context.Context, server *speedtest.Server) error {
	return server.DownloadTestContext(ctx)
}

func (d *defaultRunner) UploadTest(ctx context.Context, server *speedtest.Server) error {
	return server.UploadTestContext(ctx)
}

// defaultClient wraps speedtest.Speedtest to satisfy SpeedtestClient.
type defaultClient struct {
	inner *speedtest.Speedtest
}

func (d *defaultClient) FetchUserInfo(ctx context.Context) (*speedtest.User, error) {
	return d.inner.FetchUserInfoContext(ctx)
}

func (d *defaultClient) FetchServers(ctx context.Context) (speedtest.Servers, error) {
	return d.inner.FetchServerListContext(ctx)
}

// Exporter runs speedtest and exports them using
// the prometheus metrics package.
type Exporter struct {
	serverID       int
	serverFallback bool
	client         SpeedtestClient
	runner         ServerRunner
}

// New returns an initialized Exporter.
func New(serverID int, serverFallback bool) *Exporter {
	return &Exporter{
		serverID:       serverID,
		serverFallback: serverFallback,
		client:         &defaultClient{inner: speedtest.New()},
		runner:         &defaultRunner{},
	}
}

// NewWithDeps returns an Exporter with injected dependencies for testing.
func NewWithDeps(serverID int, serverFallback bool, client SpeedtestClient, runner ServerRunner) *Exporter {
	return &Exporter{
		serverID:       serverID,
		serverFallback: serverFallback,
		client:         client,
		runner:         runner,
	}
}

// Describe describes all the metrics. It implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- up
	ch <- scrapeDurationSeconds
	ch <- latency
	ch <- upload
	ch <- download
}

// Collect fetches the stats from a speedtest and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.CollectWithContext(context.Background(), ch)
}

// CollectWithContext is like Collect but accepts a context for cancellation.
func (e *Exporter) CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric) {
	start := time.Now()
	ok := e.speedtest(ctx, ch)

	upVal := 0.0
	if ok {
		upVal = 1.0
	}
	ch <- prometheus.MustNewConstMetric(
		up, prometheus.GaugeValue, upVal,
	)
	ch <- prometheus.MustNewConstMetric(
		scrapeDurationSeconds, prometheus.GaugeValue, time.Since(start).Seconds(),
	)
}

func (e *Exporter) speedtest(ctx context.Context, ch chan<- prometheus.Metric) bool {
	user, err := e.client.FetchUserInfo(ctx)
	if err != nil {
		slog.Error("could not fetch user information", "error", err)
		return false
	}

	servers, err := e.client.FetchServers(ctx)
	if err != nil {
		slog.Error("could not fetch server list", "error", err)
		return false
	}

	server, err := e.selectServer(servers)
	if err != nil {
		return false
	}

	ok := e.pingTest(ctx, user, server, ch)
	ok = e.downloadTest(ctx, user, server, ch) && ok
	ok = e.uploadTest(ctx, user, server, ch) && ok

	return ok
}

// selectServer picks a server based on the exporter configuration.
func (e *Exporter) selectServer(servers speedtest.Servers) (*speedtest.Server, error) {
	if len(servers) == 0 {
		return nil, fmt.Errorf("no servers available")
	}

	if e.serverID == -1 {
		return servers[0], nil
	}

	targets, err := servers.FindServer([]int{e.serverID})
	if err != nil {
		slog.Error("could not find server", "error", err)
		return nil, err
	}

	if len(targets) == 0 {
		slog.Error("no matching servers returned", "server_id", e.serverID)
		return nil, fmt.Errorf("no servers returned for ID %d", e.serverID)
	}

	if targets[0].ID != fmt.Sprintf("%d", e.serverID) && !e.serverFallback {
		slog.Error("could not find chosen server ID in available servers, server_fallback is not set so failing this test", "server_id", e.serverID)
		return nil, fmt.Errorf("server %d not found and fallback disabled", e.serverID)
	}

	return targets[0], nil
}

// labelValues returns the common label values for speedtest metrics.
func labelValues(user *speedtest.User, server *speedtest.Server) []string {
	return []string{
		user.Lat,
		user.Lon,
		user.IP,
		user.Isp,
		server.Lat,
		server.Lon,
		server.ID,
		server.Name,
		server.Country,
		fmt.Sprintf("%.0f", server.Distance),
	}
}

func (e *Exporter) pingTest(ctx context.Context, user *speedtest.User, server *speedtest.Server, ch chan<- prometheus.Metric) bool {
	err := e.runner.PingTest(ctx, server)
	if err != nil {
		slog.Error("failed to carry out ping test", "error", err)
		return false
	}

	ch <- prometheus.MustNewConstMetric(
		latency, prometheus.GaugeValue, server.Latency.Seconds(),
		labelValues(user, server)...,
	)

	return true
}

func (e *Exporter) downloadTest(ctx context.Context, user *speedtest.User, server *speedtest.Server, ch chan<- prometheus.Metric) bool {
	err := e.runner.DownloadTest(ctx, server)
	if err != nil {
		slog.Error("failed to carry out download test", "error", err)
		return false
	}

	ch <- prometheus.MustNewConstMetric(
		download, prometheus.GaugeValue, float64(server.DLSpeed),
		labelValues(user, server)...,
	)

	return true
}

func (e *Exporter) uploadTest(ctx context.Context, user *speedtest.User, server *speedtest.Server, ch chan<- prometheus.Metric) bool {
	err := e.runner.UploadTest(ctx, server)
	if err != nil {
		slog.Error("failed to carry out upload test", "error", err)
		return false
	}

	ch <- prometheus.MustNewConstMetric(
		upload, prometheus.GaugeValue, float64(server.ULSpeed),
		labelValues(user, server)...,
	)

	return true
}
