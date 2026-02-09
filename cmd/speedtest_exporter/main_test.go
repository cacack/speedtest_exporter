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

func TestHealthHandler_OK(t *testing.T) {
	// Mock server that returns 204.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer mockServer.Close()

	handler := newHealthHandler(mockServer.URL)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "OK" {
		t.Errorf("expected body 'OK', got %q", got)
	}
}

func TestHealthHandler_NoConnection(t *testing.T) {
	// Point to an unreachable URL.
	handler := newHealthHandler("http://127.0.0.1:1")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
	if got := w.Body.String(); got != "No Internet Connection" {
		t.Errorf("expected body 'No Internet Connection', got %q", got)
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
