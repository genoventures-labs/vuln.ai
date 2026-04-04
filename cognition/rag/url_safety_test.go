package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckURLSafetyAllowAndCache(t *testing.T) {
	scanCalls := 0
	resultCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/scan/":
			scanCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"uuid": "scan-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/result/scan-1/":
			resultCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"verdicts": map[string]interface{}{
					"overall": map[string]interface{}{
						"malicious":  false,
						"suspicious": false,
						"score":      0.0,
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cachePath := filepath.Join(t.TempDir(), "url_safety_cache.json")
	opts := URLSafetyOptions{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		Timeout:    10 * time.Second,
		Visibility: "private",
		CacheTTL:   24 * time.Hour,
		CachePath:  cachePath,
	}

	v1, cacheHit1, err := CheckURLSafety(context.Background(), "https://example.com/docs", opts, srv.Client())
	if err != nil {
		t.Fatalf("first safety check failed: %v", err)
	}
	if cacheHit1 {
		t.Fatal("first check should not be cache hit")
	}
	if !v1.Allowed {
		t.Fatalf("expected allowed verdict, got: %#v", v1)
	}

	v2, cacheHit2, err := CheckURLSafety(context.Background(), "https://example.com/docs", opts, srv.Client())
	if err != nil {
		t.Fatalf("second safety check failed: %v", err)
	}
	if !cacheHit2 {
		t.Fatal("second check should be cache hit")
	}
	if !v2.Allowed {
		t.Fatalf("expected allowed verdict on cache hit, got: %#v", v2)
	}
	if scanCalls != 1 || resultCalls != 1 {
		t.Fatalf("expected one network scan/result cycle, got scanCalls=%d resultCalls=%d", scanCalls, resultCalls)
	}
}

func TestParseURLSafetyVerdictBlockedSuspicious(t *testing.T) {
	verdict, err := parseURLSafetyVerdict("scan-1", "https://urlscan.io/api/v1/result/scan-1/", map[string]interface{}{
		"verdicts": map[string]interface{}{
			"overall": map[string]interface{}{
				"malicious":  false,
				"suspicious": true,
				"score":      0.0,
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Allowed {
		t.Fatalf("expected suspicious verdict to be blocked: %#v", verdict)
	}
}

func TestParseURLSafetyVerdictUnavailable(t *testing.T) {
	if _, err := parseURLSafetyVerdict("scan-2", "https://urlscan.io/api/v1/result/scan-2/", map[string]interface{}{}); err == nil {
		t.Fatal("expected error for missing verdict fields")
	}
}
