package exporter

import (
	"context"
	"errors"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/showwin/speedtest-go/speedtest"
)

// mockClient implements SpeedtestClient for testing.
type mockClient struct {
	user       *speedtest.User
	userErr    error
	servers    speedtest.Servers
	serversErr error
}

func (m *mockClient) FetchUserInfo(_ context.Context) (*speedtest.User, error) {
	return m.user, m.userErr
}

func (m *mockClient) FetchServers(_ context.Context) (speedtest.Servers, error) {
	return m.servers, m.serversErr
}

// mockRunner implements ServerRunner for testing.
type mockRunner struct {
	pingErr     error
	downloadErr error
	uploadErr   error
	// Values to set on the server when test succeeds.
	latency time.Duration
	dlSpeed speedtest.ByteRate
	ulSpeed speedtest.ByteRate
}

func (m *mockRunner) PingTest(_ context.Context, server *speedtest.Server) error {
	if m.pingErr != nil {
		return m.pingErr
	}
	server.Latency = m.latency
	return nil
}

func (m *mockRunner) DownloadTest(_ context.Context, server *speedtest.Server) error {
	if m.downloadErr != nil {
		return m.downloadErr
	}
	server.DLSpeed = m.dlSpeed
	return nil
}

func (m *mockRunner) UploadTest(_ context.Context, server *speedtest.Server) error {
	if m.uploadErr != nil {
		return m.uploadErr
	}
	server.ULSpeed = m.ulSpeed
	return nil
}

func newTestUser() *speedtest.User {
	return &speedtest.User{
		IP:  "1.2.3.4",
		Lat: "40.7128",
		Lon: "-74.0060",
		Isp: "TestISP",
	}
}

func newTestServer(id string) *speedtest.Server {
	return &speedtest.Server{
		ID:       id,
		Name:     "TestServer",
		Country:  "US",
		Lat:      "34.0522",
		Lon:      "-118.2437",
		Distance: 123.456,
	}
}

func newTestRunner() *mockRunner {
	return &mockRunner{
		latency: 10 * time.Millisecond,
		dlSpeed: 100000000, // 100,000,000 B/s (~800 Mbps)
		ulSpeed: 50000000,  // 50,000,000 B/s (~400 Mbps)
	}
}

// collectMetrics gathers all metrics from a Collect call.
func collectMetrics(e *Exporter) []prometheus.Metric {
	ch := make(chan prometheus.Metric, 100)
	go func() {
		e.Collect(ch)
		close(ch)
	}()

	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}
	return metrics
}

// metricToDTO converts a prometheus.Metric to a DTO for inspection.
func metricToDTO(m prometheus.Metric) *dto.Metric {
	d := &dto.Metric{}
	_ = m.Write(d)
	return d
}

// findMetricByName finds a metric by its fqName in a slice.
func findMetricByName(metrics []prometheus.Metric, name string) prometheus.Metric {
	// Desc().String() looks like: Desc{fqName: "speedtest_up", ...}
	// Use quoted form to avoid partial matches (e.g., "speedtest_up" vs "speedtest_upload_speed_bytes_per_second").
	needle := `"` + name + `"`
	for _, m := range metrics {
		desc := m.Desc().String()
		if contains(desc, needle) {
			return m
		}
	}
	return nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestDescribe(t *testing.T) {
	e := NewWithDeps(-1, false, &mockClient{}, &mockRunner{})
	ch := make(chan *prometheus.Desc, 10)
	e.Describe(ch)
	close(ch)

	var descs []*prometheus.Desc
	for d := range ch {
		descs = append(descs, d)
	}

	if got := len(descs); got != 5 {
		t.Fatalf("expected 5 descriptors, got %d", got)
	}

	expected := []string{
		"speedtest_up",
		"speedtest_scrape_duration_seconds",
		"speedtest_latency_seconds",
		"speedtest_upload_speed_bytes_per_second",
		"speedtest_download_speed_bytes_per_second",
	}
	for _, name := range expected {
		found := false
		for _, d := range descs {
			if contains(d.String(), name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected descriptor %q not found", name)
		}
	}
}

func TestCollect_Success(t *testing.T) {
	client := &mockClient{
		user:    newTestUser(),
		servers: speedtest.Servers{newTestServer("100")},
	}
	runner := newTestRunner()
	e := NewWithDeps(-1, false, client, runner)

	metrics := collectMetrics(e)

	// Expect: latency + download + upload + up + scrape_duration = 5
	if got := len(metrics); got != 5 {
		t.Fatalf("expected 5 metrics, got %d", got)
	}

	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	d := metricToDTO(upMetric)
	if got := d.GetGauge().GetValue(); got != 1.0 {
		t.Errorf("expected up=1.0, got %f", got)
	}

	// Verify speed metrics pass through bytes/sec values from speedtest-go unchanged.
	dlMetric := findMetricByName(metrics, "speedtest_download_speed_bytes_per_second")
	if dlMetric == nil {
		t.Fatal("speedtest_download_speed_bytes_per_second metric not found")
	}
	if got := metricToDTO(dlMetric).GetGauge().GetValue(); got != float64(runner.dlSpeed) {
		t.Errorf("expected download=%f, got %f", float64(runner.dlSpeed), got)
	}

	ulMetric := findMetricByName(metrics, "speedtest_upload_speed_bytes_per_second")
	if ulMetric == nil {
		t.Fatal("speedtest_upload_speed_bytes_per_second metric not found")
	}
	if got := metricToDTO(ulMetric).GetGauge().GetValue(); got != float64(runner.ulSpeed) {
		t.Errorf("expected upload=%f, got %f", float64(runner.ulSpeed), got)
	}
}

func TestCollect_FetchUserInfoError(t *testing.T) {
	client := &mockClient{
		userErr: errors.New("network error"),
	}
	e := NewWithDeps(-1, false, client, &mockRunner{})

	metrics := collectMetrics(e)

	// up + scrape_duration should always be emitted.
	if got := len(metrics); got != 2 {
		t.Fatalf("expected 2 metrics, got %d", got)
	}
	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	if got := metricToDTO(upMetric).GetGauge().GetValue(); got != 0.0 {
		t.Errorf("expected up=0.0, got %f", got)
	}
	if findMetricByName(metrics, "speedtest_scrape_duration_seconds") == nil {
		t.Fatal("speedtest_scrape_duration_seconds metric not found")
	}
}

func TestCollect_FetchServersError(t *testing.T) {
	client := &mockClient{
		user:       newTestUser(),
		serversErr: errors.New("server list unavailable"),
	}
	e := NewWithDeps(-1, false, client, &mockRunner{})

	metrics := collectMetrics(e)

	if got := len(metrics); got != 2 {
		t.Fatalf("expected 2 metrics, got %d", got)
	}
	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	if got := metricToDTO(upMetric).GetGauge().GetValue(); got != 0.0 {
		t.Errorf("expected up=0.0, got %f", got)
	}
	if findMetricByName(metrics, "speedtest_scrape_duration_seconds") == nil {
		t.Fatal("speedtest_scrape_duration_seconds metric not found")
	}
}

func TestSelectServer_ClosestServer(t *testing.T) {
	servers := speedtest.Servers{
		newTestServer("1"),
		newTestServer("2"),
	}
	e := NewWithDeps(-1, false, &mockClient{}, &mockRunner{})

	server, err := e.selectServer(servers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if server.ID != "1" {
		t.Errorf("expected server ID '1', got %q", server.ID)
	}
}

func TestSelectServer_SpecificServer(t *testing.T) {
	servers := speedtest.Servers{
		newTestServer("100"),
		newTestServer("200"),
	}
	e := NewWithDeps(200, false, &mockClient{}, &mockRunner{})

	server, err := e.selectServer(servers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if server.ID != "200" {
		t.Errorf("expected server ID '200', got %q", server.ID)
	}
}

func TestSelectServer_FallbackEnabled(t *testing.T) {
	// Request server 999 which doesn't exist; fallback=true should use the returned server.
	servers := speedtest.Servers{
		newTestServer("100"),
	}
	e := NewWithDeps(999, true, &mockClient{}, &mockRunner{})

	server, err := e.selectServer(servers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FindServer returns the closest available when ID not found.
	if server == nil {
		t.Fatal("expected a server, got nil")
	}
}

func TestSelectServer_EmptyServers(t *testing.T) {
	servers := speedtest.Servers{}
	e := NewWithDeps(-1, false, &mockClient{}, &mockRunner{})

	_, err := e.selectServer(servers)
	if err == nil {
		t.Fatal("expected error for empty server list")
	}
}

func TestCollect_EmptyServerList(t *testing.T) {
	client := &mockClient{
		user:    newTestUser(),
		servers: speedtest.Servers{},
	}
	e := NewWithDeps(-1, false, client, newTestRunner())

	metrics := collectMetrics(e)

	if got := len(metrics); got != 2 {
		t.Fatalf("expected 2 metrics, got %d", got)
	}
	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	if got := metricToDTO(upMetric).GetGauge().GetValue(); got != 0.0 {
		t.Errorf("expected up=0.0, got %f", got)
	}
	if findMetricByName(metrics, "speedtest_scrape_duration_seconds") == nil {
		t.Fatal("speedtest_scrape_duration_seconds metric not found")
	}
}

func TestSelectServer_FallbackDisabled(t *testing.T) {
	// Request server 999 which doesn't exist; fallback=false should error.
	servers := speedtest.Servers{
		newTestServer("100"),
	}
	e := NewWithDeps(999, false, &mockClient{}, &mockRunner{})

	_, err := e.selectServer(servers)
	if err == nil {
		t.Fatal("expected error when server not found and fallback disabled")
	}
}

func TestCollect_PingFailure(t *testing.T) {
	client := &mockClient{
		user:    newTestUser(),
		servers: speedtest.Servers{newTestServer("100")},
	}
	runner := &mockRunner{
		pingErr: errors.New("ping failed"),
		dlSpeed: 100000000,
		ulSpeed: 50000000,
	}
	e := NewWithDeps(-1, false, client, runner)

	metrics := collectMetrics(e)

	// Ping fails but download/upload still attempted.
	// download + upload + up + scrape_duration = 4 (no latency)
	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	d := metricToDTO(upMetric)
	if got := d.GetGauge().GetValue(); got != 0.0 {
		t.Errorf("expected up=0.0, got %f", got)
	}
	if findMetricByName(metrics, "speedtest_scrape_duration_seconds") == nil {
		t.Fatal("speedtest_scrape_duration_seconds metric not found")
	}
}

func TestCollect_DownloadFailure(t *testing.T) {
	client := &mockClient{
		user:    newTestUser(),
		servers: speedtest.Servers{newTestServer("100")},
	}
	runner := &mockRunner{
		latency:     10 * time.Millisecond,
		downloadErr: errors.New("download failed"),
		ulSpeed:     50000000,
	}
	e := NewWithDeps(-1, false, client, runner)

	metrics := collectMetrics(e)

	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	d := metricToDTO(upMetric)
	if got := d.GetGauge().GetValue(); got != 0.0 {
		t.Errorf("expected up=0.0, got %f", got)
	}
	if findMetricByName(metrics, "speedtest_scrape_duration_seconds") == nil {
		t.Fatal("speedtest_scrape_duration_seconds metric not found")
	}
}

func TestCollect_UploadFailure(t *testing.T) {
	client := &mockClient{
		user:    newTestUser(),
		servers: speedtest.Servers{newTestServer("100")},
	}
	runner := &mockRunner{
		latency:   10 * time.Millisecond,
		dlSpeed:   100000000,
		uploadErr: errors.New("upload failed"),
	}
	e := NewWithDeps(-1, false, client, runner)

	metrics := collectMetrics(e)

	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	d := metricToDTO(upMetric)
	if got := d.GetGauge().GetValue(); got != 0.0 {
		t.Errorf("expected up=0.0, got %f", got)
	}
	if findMetricByName(metrics, "speedtest_scrape_duration_seconds") == nil {
		t.Fatal("speedtest_scrape_duration_seconds metric not found")
	}
}

func TestCollect_MetricLabels(t *testing.T) {
	user := newTestUser()
	server := newTestServer("100")
	client := &mockClient{
		user:    user,
		servers: speedtest.Servers{server},
	}
	runner := newTestRunner()
	e := NewWithDeps(-1, false, client, runner)

	// Use a registry to gather and inspect metrics with full label detail.
	reg := prometheus.NewRegistry()
	reg.MustRegister(e)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	// Build a map of metric family name -> metric family for easy lookup.
	familyMap := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		familyMap[f.GetName()] = f
	}

	// Verify latency metric labels.
	latencyFamily, ok := familyMap["speedtest_latency_seconds"]
	if !ok {
		t.Fatal("speedtest_latency_seconds family not found")
	}
	if len(latencyFamily.GetMetric()) != 1 {
		t.Fatalf("expected 1 latency metric, got %d", len(latencyFamily.GetMetric()))
	}

	labelMap := make(map[string]string)
	for _, lp := range latencyFamily.GetMetric()[0].GetLabel() {
		labelMap[lp.GetName()] = lp.GetValue()
	}

	expectedLabels := map[string]string{
		"user_ip":        "1.2.3.4",
		"user_lat":       "40.7128",
		"user_lon":       "-74.0060",
		"user_isp":       "TestISP",
		"server_lat":     "34.0522",
		"server_lon":     "-118.2437",
		"server_id":      "100",
		"server_name":    "TestServer",
		"server_country": "US",
		"distance":       "123",
	}
	for k, want := range expectedLabels {
		if got, exists := labelMap[k]; !exists {
			t.Errorf("label %q not found", k)
		} else if got != want {
			t.Errorf("label %q: got %q, want %q", k, got, want)
		}
	}
}

// ctxAwareRunner is a mock runner that checks ctx.Err() before proceeding.
type ctxAwareRunner struct {
	mockRunner
}

func (m *ctxAwareRunner) PingTest(ctx context.Context, server *speedtest.Server) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.mockRunner.PingTest(ctx, server)
}

func (m *ctxAwareRunner) DownloadTest(ctx context.Context, server *speedtest.Server) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.mockRunner.DownloadTest(ctx, server)
}

func (m *ctxAwareRunner) UploadTest(ctx context.Context, server *speedtest.Server) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.mockRunner.UploadTest(ctx, server)
}

func TestCollect_ContextCancellation(t *testing.T) {
	client := &mockClient{
		user:    newTestUser(),
		servers: speedtest.Servers{newTestServer("100")},
	}
	runner := &ctxAwareRunner{
		mockRunner: *newTestRunner(),
	}
	e := NewWithDeps(-1, false, client, runner)

	// Create an already-cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan prometheus.Metric, 100)
	e.CollectWithContext(ctx, ch)
	close(ch)

	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}

	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	d := metricToDTO(upMetric)
	if got := d.GetGauge().GetValue(); got != 0.0 {
		t.Errorf("expected up=0.0 for cancelled context, got %f", got)
	}
}
