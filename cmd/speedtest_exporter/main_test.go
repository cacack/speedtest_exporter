package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRootHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	rootHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !containsString(body, "Speedtest Exporter") {
		t.Error("response body missing title")
	}
	if !containsString(body, metricsPath) {
		t.Error("response body missing metrics link")
	}
	if !containsString(body, "/health") {
		t.Error("response body missing health link")
	}
}

func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	healthHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "OK" {
		t.Errorf("expected body 'OK', got %q", got)
	}
}

func TestParseServerIDs(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []int
		wantErr bool
	}{
		{name: "single ID", input: "12345", want: []int{12345}},
		{name: "default closest", input: "-1", want: []int{-1}},
		{name: "multiple IDs", input: "100,200,300", want: []int{100, 200, 300}},
		{name: "spaces around IDs", input: " 100 , 200 , 300 ", want: []int{100, 200, 300}},
		{name: "empty string", input: "", wantErr: true},
		{name: "non-numeric", input: "abc", wantErr: true},
		{name: "mixed valid and invalid", input: "100,abc", wantErr: true},
		{name: "trailing comma", input: "100,200,", want: []int{100, 200}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServerIDs(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("index %d: expected %d, got %d", i, tt.want[i], got[i])
				}
			}
		})
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
