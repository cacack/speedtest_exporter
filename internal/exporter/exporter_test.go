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
	e := NewWithDeps([]int{-1}, false, &mockClient{}, &mockRunner{})
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
	e := NewWithDeps([]int{-1}, false, client, runner)

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
	e := NewWithDeps([]int{-1}, false, client, &mockRunner{})

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
	e := NewWithDeps([]int{-1}, false, client, &mockRunner{})

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

func TestSelectServers_ClosestServer(t *testing.T) {
	servers := speedtest.Servers{
		newTestServer("1"),
		newTestServer("2"),
	}
	e := NewWithDeps([]int{-1}, false, &mockClient{}, &mockRunner{})

	result, err := e.selectServers(servers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0].ID != "1" {
		t.Errorf("expected single server with ID '1', got %v", result)
	}
}

func TestSelectServers_SpecificServer(t *testing.T) {
	servers := speedtest.Servers{
		newTestServer("100"),
		newTestServer("200"),
	}
	e := NewWithDeps([]int{200}, false, &mockClient{}, &mockRunner{})

	result, err := e.selectServers(servers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0].ID != "200" {
		t.Errorf("expected single server with ID '200', got %v", result)
	}
}

func TestSelectServers_MultipleIDs(t *testing.T) {
	servers := speedtest.Servers{
		newTestServer("100"),
		newTestServer("200"),
		newTestServer("300"),
	}
	e := NewWithDeps([]int{100, 300}, false, &mockClient{}, &mockRunner{})

	result, err := e.selectServers(servers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(result))
	}
	ids := map[string]bool{}
	for _, s := range result {
		ids[s.ID] = true
	}
	if !ids["100"] || !ids["300"] {
		t.Errorf("expected servers 100 and 300, got %v", ids)
	}
}

func TestSelectServers_FallbackEnabled(t *testing.T) {
	// Request server 999 which doesn't exist; fallback=true should use the returned server.
	servers := speedtest.Servers{
		newTestServer("100"),
	}
	e := NewWithDeps([]int{999}, true, &mockClient{}, &mockRunner{})

	result, err := e.selectServers(servers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FindServer returns the closest available when ID not found.
	if len(result) == 0 {
		t.Fatal("expected at least one server, got none")
	}
}

func TestSelectServers_EmptyServers(t *testing.T) {
	servers := speedtest.Servers{}
	e := NewWithDeps([]int{-1}, false, &mockClient{}, &mockRunner{})

	_, err := e.selectServers(servers)
	if err == nil {
		t.Fatal("expected error for empty server list")
	}
}

func TestSelectServers_MissingID_FallbackDisabled(t *testing.T) {
	servers := speedtest.Servers{
		newTestServer("100"),
		newTestServer("200"),
	}
	// Request IDs 100 and 999; 999 is missing, fallback disabled.
	e := NewWithDeps([]int{100, 999}, false, &mockClient{}, &mockRunner{})

	_, err := e.selectServers(servers)
	if err == nil {
		t.Fatal("expected error when requested server ID missing and fallback disabled")
	}
}

func TestCollect_EmptyServerList(t *testing.T) {
	client := &mockClient{
		user:    newTestUser(),
		servers: speedtest.Servers{},
	}
	e := NewWithDeps([]int{-1}, false, client, newTestRunner())

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

func TestSelectServers_FallbackDisabled(t *testing.T) {
	// Request server 999 which doesn't exist; fallback=false should error.
	servers := speedtest.Servers{
		newTestServer("100"),
	}
	e := NewWithDeps([]int{999}, false, &mockClient{}, &mockRunner{})

	_, err := e.selectServers(servers)
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
	e := NewWithDeps([]int{-1}, false, client, runner)

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
	e := NewWithDeps([]int{-1}, false, client, runner)

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
	e := NewWithDeps([]int{-1}, false, client, runner)

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
	e := NewWithDeps([]int{-1}, false, client, runner)

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
	e := NewWithDeps([]int{-1}, false, client, runner)

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

// findAllMetricsByName finds all metrics matching the given fqName.
func findAllMetricsByName(metrics []prometheus.Metric, name string) []prometheus.Metric {
	needle := `"` + name + `"`
	var result []prometheus.Metric
	for _, m := range metrics {
		if contains(m.Desc().String(), needle) {
			result = append(result, m)
		}
	}
	return result
}

func TestCollect_MultipleServers_AllSuccess(t *testing.T) {
	client := &mockClient{
		user: newTestUser(),
		servers: speedtest.Servers{
			newTestServer("100"),
			newTestServer("200"),
		},
	}
	runner := newTestRunner()
	e := NewWithDeps([]int{100, 200}, false, client, runner)

	metrics := collectMetrics(e)

	// 2 servers x 3 metrics each (latency, download, upload) + up + scrape_duration = 8
	if got := len(metrics); got != 8 {
		t.Fatalf("expected 8 metrics, got %d", got)
	}

	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	if got := metricToDTO(upMetric).GetGauge().GetValue(); got != 1.0 {
		t.Errorf("expected up=1.0, got %f", got)
	}

	// Verify we got 2 latency, 2 download, 2 upload metrics.
	if got := len(findAllMetricsByName(metrics, "speedtest_latency_seconds")); got != 2 {
		t.Errorf("expected 2 latency metrics, got %d", got)
	}
	if got := len(findAllMetricsByName(metrics, "speedtest_download_speed_bytes_per_second")); got != 2 {
		t.Errorf("expected 2 download metrics, got %d", got)
	}
	if got := len(findAllMetricsByName(metrics, "speedtest_upload_speed_bytes_per_second")); got != 2 {
		t.Errorf("expected 2 upload metrics, got %d", got)
	}
}

func TestCollect_MultipleServers_PartialFailure(t *testing.T) {
	client := &mockClient{
		user: newTestUser(),
		servers: speedtest.Servers{
			newTestServer("100"),
			newTestServer("200"),
		},
	}
	// Ping fails — affects both servers since it's the same runner.
	// To simulate partial failure, use a runner that fails only for specific servers.
	runner := &perServerMockRunner{
		results: map[string]mockRunner{
			"100": {
				latency: 10 * time.Millisecond,
				dlSpeed: 100000000,
				ulSpeed: 50000000,
			},
			"200": {
				pingErr: errors.New("ping failed"),
			},
		},
	}
	e := NewWithDeps([]int{100, 200}, false, client, runner)

	metrics := collectMetrics(e)

	upMetric := findMetricByName(metrics, "speedtest_up")
	if upMetric == nil {
		t.Fatal("speedtest_up metric not found")
	}
	if got := metricToDTO(upMetric).GetGauge().GetValue(); got != 0.0 {
		t.Errorf("expected up=0.0 for partial failure, got %f", got)
	}

	// Server 100 should have all 3 metrics; server 200 should have 0 (ping failed, no latency/download/upload emitted for it... actually download/upload still attempted).
	// With current logic: server 200 ping fails -> no latency, but download/upload still run.
	// Server 100: latency + download + upload = 3
	// Server 200: download + upload = 2 (no latency since ping failed)
	// Total: 3 + 2 + up + scrape_duration = 7
	if got := len(metrics); got != 7 {
		t.Fatalf("expected 7 metrics, got %d", got)
	}
}

// perServerMockRunner provides different mock results per server ID.
type perServerMockRunner struct {
	results map[string]mockRunner
}

func (p *perServerMockRunner) PingTest(ctx context.Context, server *speedtest.Server) error {
	r := p.results[server.ID]
	if r.pingErr != nil {
		return r.pingErr
	}
	server.Latency = r.latency
	return nil
}

func (p *perServerMockRunner) DownloadTest(ctx context.Context, server *speedtest.Server) error {
	r := p.results[server.ID]
	if r.downloadErr != nil {
		return r.downloadErr
	}
	server.DLSpeed = r.dlSpeed
	return nil
}

func (p *perServerMockRunner) UploadTest(ctx context.Context, server *speedtest.Server) error {
	r := p.results[server.ID]
	if r.uploadErr != nil {
		return r.uploadErr
	}
	server.ULSpeed = r.ulSpeed
	return nil
}
