package exporter

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/showwin/speedtest-go/speedtest"
)

const (
	namespace = "speedtest"
)

var (
	up = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "up"),
		"Was the last speedtest successful.",
		[]string{"test_uuid"}, nil,
	)
	scrapeDurationSeconds = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "scrape_duration_seconds"),
		"Time to perform last speed test",
		[]string{"test_uuid"}, nil,
	)
	latency = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "latency_seconds"),
		"Measured latency on last speed test",
		[]string{"test_uuid", "user_lat", "user_lon", "user_ip", "user_isp", "server_lat", "server_lon", "server_id", "server_name", "server_country", "distance"},
		nil,
	)
	upload = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "upload_speed_Bps"),
		"Last upload speedtest result",
		[]string{"test_uuid", "user_lat", "user_lon", "user_ip", "user_isp", "server_lat", "server_lon", "server_id", "server_name", "server_country", "distance"},
		nil,
	)
	download = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "download_speed_Bps"),
		"Last download speedtest result",
		[]string{"test_uuid", "user_lat", "user_lon", "user_ip", "user_isp", "server_lat", "server_lon", "server_id", "server_name", "server_country", "distance"},
		nil,
	)
)

// SpeedtestClient abstracts the speedtest-go client.
type SpeedtestClient interface {
	FetchUserInfo() (*speedtest.User, error)
	FetchServers() (speedtest.Servers, error)
}

// ServerRunner abstracts speed test execution on a server.
type ServerRunner interface {
	PingTest(server *speedtest.Server) error
	DownloadTest(server *speedtest.Server) error
	UploadTest(server *speedtest.Server) error
}

// defaultRunner calls the real speedtest server methods.
type defaultRunner struct{}

func (d *defaultRunner) PingTest(server *speedtest.Server) error {
	return server.PingTest(nil)
}

func (d *defaultRunner) DownloadTest(server *speedtest.Server) error {
	return server.DownloadTest()
}

func (d *defaultRunner) UploadTest(server *speedtest.Server) error {
	return server.UploadTest()
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
func New(serverID int, serverFallback bool) (*Exporter, error) {
	return &Exporter{
		serverID:       serverID,
		serverFallback: serverFallback,
		client:         speedtest.New(),
		runner:         &defaultRunner{},
	}, nil
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
	testUUID := uuid.New().String()
	start := time.Now()
	ok := e.speedtest(testUUID, ch)

	if ok {
		ch <- prometheus.MustNewConstMetric(
			up, prometheus.GaugeValue, 1.0,
			testUUID,
		)
		ch <- prometheus.MustNewConstMetric(
			scrapeDurationSeconds, prometheus.GaugeValue, time.Since(start).Seconds(),
			testUUID,
		)
	} else {
		ch <- prometheus.MustNewConstMetric(
			up, prometheus.GaugeValue, 0.0,
			testUUID,
		)
	}
}

func (e *Exporter) speedtest(testUUID string, ch chan<- prometheus.Metric) bool {
	user, err := e.client.FetchUserInfo()
	if err != nil {
		slog.Error("could not fetch user information", "error", err)
		return false
	}

	servers, err := e.client.FetchServers()
	if err != nil {
		slog.Error("could not fetch server list", "error", err)
		return false
	}

	server, err := e.selectServer(servers)
	if err != nil {
		return false
	}

	ok := e.pingTest(testUUID, user, server, ch)
	ok = e.downloadTest(testUUID, user, server, ch) && ok
	ok = e.uploadTest(testUUID, user, server, ch) && ok

	return ok
}

// selectServer picks a server based on the exporter configuration.
func (e *Exporter) selectServer(servers speedtest.Servers) (*speedtest.Server, error) {
	if e.serverID == -1 {
		return servers[0], nil
	}

	targets, err := servers.FindServer([]int{e.serverID})
	if err != nil {
		slog.Error("could not find server", "error", err)
		return nil, err
	}

	if targets[0].ID != fmt.Sprintf("%d", e.serverID) && !e.serverFallback {
		slog.Error("could not find chosen server ID in available servers, server_fallback is not set so failing this test", "server_id", e.serverID)
		return nil, fmt.Errorf("server %d not found and fallback disabled", e.serverID)
	}

	return targets[0], nil
}

func (e *Exporter) pingTest(testUUID string, user *speedtest.User, server *speedtest.Server, ch chan<- prometheus.Metric) bool {
	err := e.runner.PingTest(server)
	if err != nil {
		slog.Error("failed to carry out ping test", "error", err)
		return false
	}

	ch <- prometheus.MustNewConstMetric(
		latency, prometheus.GaugeValue, server.Latency.Seconds(),
		testUUID,
		user.Lat,
		user.Lon,
		user.IP,
		user.Isp,
		server.Lat,
		server.Lon,
		server.ID,
		server.Name,
		server.Country,
		fmt.Sprintf("%f", server.Distance),
	)

	return true
}

func (e *Exporter) downloadTest(testUUID string, user *speedtest.User, server *speedtest.Server, ch chan<- prometheus.Metric) bool {
	err := e.runner.DownloadTest(server)
	if err != nil {
		slog.Error("failed to carry out download test", "error", err)
		return false
	}

	ch <- prometheus.MustNewConstMetric(
		download, prometheus.GaugeValue, float64(server.DLSpeed),
		testUUID,
		user.Lat,
		user.Lon,
		user.IP,
		user.Isp,
		server.Lat,
		server.Lon,
		server.ID,
		server.Name,
		server.Country,
		fmt.Sprintf("%f", server.Distance),
	)

	return true
}

func (e *Exporter) uploadTest(testUUID string, user *speedtest.User, server *speedtest.Server, ch chan<- prometheus.Metric) bool {
	err := e.runner.UploadTest(server)
	if err != nil {
		slog.Error("failed to carry out upload test", "error", err)
		return false
	}

	ch <- prometheus.MustNewConstMetric(
		upload, prometheus.GaugeValue, float64(server.ULSpeed),
		testUUID,
		user.Lat,
		user.Lon,
		user.IP,
		user.Isp,
		server.Lat,
		server.Lon,
		server.ID,
		server.Name,
		server.Country,
		fmt.Sprintf("%f", server.Distance),
	)

	return true
}
