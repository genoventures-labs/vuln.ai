package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultURLSafetyBaseURL = "https://urlscan.io"
	defaultURLSafetyPoll    = 2 * time.Second
)

type URLSafetyOptions struct {
	APIKey     string
	BaseURL    string
	Timeout    time.Duration
	Visibility string
	CacheTTL   time.Duration
	FailOpen   bool
	CachePath  string
}

type URLSafetyVerdict struct {
	Allowed    bool
	Status     string
	Reason     string
	ScanID     string
	ResultURL  string
	Overall    string
	Malicious  bool
	Suspicious bool
}

type urlSafetyCacheEntry struct {
	CheckedAt time.Time        `json:"checked_at"`
	ExpiresAt time.Time        `json:"expires_at"`
	URL       string           `json:"url"`
	Verdict   URLSafetyVerdict `json:"verdict"`
}

type urlSafetyCache struct {
	Entries map[string]urlSafetyCacheEntry `json:"entries"`
}

func CheckURLSafety(ctx context.Context, normalizedURL string, opts URLSafetyOptions, client *http.Client) (URLSafetyVerdict, bool, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return URLSafetyVerdict{}, false, fmt.Errorf("url safety api key missing")
	}
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = defaultURLSafetyBaseURL
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 45 * time.Second
	}
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = 24 * time.Hour
	}
	if strings.TrimSpace(opts.Visibility) == "" {
		opts.Visibility = "private"
	}
	if strings.TrimSpace(opts.CachePath) == "" {
		opts.CachePath = filepath.Join(".memory", "url_safety_cache.json")
	}
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}

	cache, _ := loadURLSafetyCache(opts.CachePath)
	now := time.Now().UTC()
	if entry, ok := cache.Entries[normalizedURL]; ok && now.Before(entry.ExpiresAt) {
		return entry.Verdict, true, nil
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	scanID, err := submitURLScan(ctx, client, normalizedURL, opts)
	if err != nil {
		return URLSafetyVerdict{}, false, err
	}
	verdict, err := pollURLScanResult(ctx, client, scanID, opts)
	if err != nil {
		return URLSafetyVerdict{}, false, err
	}

	cache.Entries[normalizedURL] = urlSafetyCacheEntry{
		CheckedAt: now,
		ExpiresAt: now.Add(opts.CacheTTL),
		URL:       normalizedURL,
		Verdict:   verdict,
	}
	_ = saveURLSafetyCache(opts.CachePath, cache)

	return verdict, false, nil
}

func submitURLScan(ctx context.Context, client *http.Client, targetURL string, opts URLSafetyOptions) (string, error) {
	base := strings.TrimRight(opts.BaseURL, "/")
	endpoint := base + "/api/v1/scan/"

	body := map[string]string{
		"url":        targetURL,
		"visibility": opts.Visibility,
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", opts.APIKey)
	req.Header.Set("User-Agent", "talos/1.0 (+https://thynaptic.com)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("urlscan submit failed: status %d", resp.StatusCode)
	}

	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	uuid := strings.TrimSpace(getStringAny(out, "uuid"))
	if uuid == "" {
		return "", fmt.Errorf("urlscan submit missing scan uuid")
	}
	return uuid, nil
}

func pollURLScanResult(ctx context.Context, client *http.Client, scanID string, opts URLSafetyOptions) (URLSafetyVerdict, error) {
	base := strings.TrimRight(opts.BaseURL, "/")
	endpoint := base + "/api/v1/result/" + url.PathEscape(scanID) + "/"
	ticker := time.NewTicker(defaultURLSafetyPoll)
	defer ticker.Stop()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return URLSafetyVerdict{}, err
		}
		req.Header.Set("api-key", opts.APIKey)
		req.Header.Set("User-Agent", "talos/1.0 (+https://thynaptic.com)")
		resp, err := client.Do(req)
		if err != nil {
			return URLSafetyVerdict{}, err
		}

		if resp.StatusCode == http.StatusOK {
			var out map[string]interface{}
			decodeErr := json.NewDecoder(resp.Body).Decode(&out)
			_ = resp.Body.Close()
			if decodeErr != nil {
				return URLSafetyVerdict{}, decodeErr
			}
			return parseURLSafetyVerdict(scanID, endpoint, out)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound &&
			resp.StatusCode != http.StatusAccepted &&
			resp.StatusCode != http.StatusTooManyRequests {
			return URLSafetyVerdict{}, fmt.Errorf("urlscan poll failed: status %d", resp.StatusCode)
		}

		select {
		case <-ctx.Done():
			return URLSafetyVerdict{}, fmt.Errorf("urlscan polling timed out")
		case <-ticker.C:
		}
	}
}

func parseURLSafetyVerdict(scanID, resultURL string, payload map[string]interface{}) (URLSafetyVerdict, error) {
	mal := getBoolNested(payload, "verdicts", "overall", "malicious")
	susp := getBoolNested(payload, "verdicts", "overall", "suspicious")
	score, scoreOK := getFloatNested(payload, "verdicts", "overall", "score")

	if scoreOK && score > 0 {
		susp = true
	}
	if !mal && !susp && !scoreOK {
		return URLSafetyVerdict{}, fmt.Errorf("urlscan verdict unavailable")
	}
	v := URLSafetyVerdict{
		Allowed:    !mal && !susp,
		Status:     "allowed",
		Reason:     "benign verdict",
		ScanID:     scanID,
		ResultURL:  resultURL,
		Overall:    strconv.FormatFloat(score, 'f', 4, 64),
		Malicious:  mal,
		Suspicious: susp,
	}
	if mal {
		v.Status = "blocked"
		v.Reason = "malicious verdict"
	}
	if susp && !mal {
		v.Status = "blocked"
		v.Reason = "suspicious verdict"
	}
	return v, nil
}

func loadURLSafetyCache(path string) (urlSafetyCache, error) {
	cache := urlSafetyCache{Entries: make(map[string]urlSafetyCacheEntry)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cache, nil
		}
		return cache, err
	}
	if len(data) == 0 {
		return cache, nil
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return urlSafetyCache{Entries: make(map[string]urlSafetyCacheEntry)}, err
	}
	if cache.Entries == nil {
		cache.Entries = make(map[string]urlSafetyCacheEntry)
	}
	return cache, nil
}

func saveURLSafetyCache(path string, cache urlSafetyCache) error {
	if cache.Entries == nil {
		cache.Entries = make(map[string]urlSafetyCacheEntry)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func getStringAny(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func getBoolNested(m map[string]interface{}, path ...string) bool {
	var cur interface{} = m
	for _, p := range path {
		asMap, ok := cur.(map[string]interface{})
		if !ok {
			return false
		}
		cur = asMap[p]
	}
	b, _ := cur.(bool)
	return b
}

func getFloatNested(m map[string]interface{}, path ...string) (float64, bool) {
	var cur interface{} = m
	for _, p := range path {
		asMap, ok := cur.(map[string]interface{})
		if !ok {
			return 0, false
		}
		cur = asMap[p]
	}
	switch v := cur.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}
