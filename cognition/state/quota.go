package state

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultQuotaUsagePath    = ".memory/quota_usage.json"
	defaultDailyQuotaLimit   = 1200
	defaultQuotaWarnRatio    = 0.80
	quotaHeaderRemaining     = "x-goog-quota-remaining"
	quotaHeaderRemainingAlt1 = "x-goog-quota-used"
)

var quotaMu sync.Mutex

// QuotaUsage tracks request consumption against a daily budget.
type QuotaUsage struct {
	Date               string    `json:"date"`
	DailyLimit         int       `json:"daily_limit"`
	RequestsUsed       int       `json:"requests_used"`
	LastKnownRemaining int       `json:"last_known_remaining,omitempty"`
	LastHeaderSeen     string    `json:"last_header_seen,omitempty"`
	LastUpdated        time.Time `json:"last_updated"`
}

// QuotaSnapshot is a compact read model for daemon/safety checks.
type QuotaSnapshot struct {
	Date             string    `json:"date"`
	DailyLimit       int       `json:"daily_limit"`
	RequestsUsed     int       `json:"requests_used"`
	Remaining        int       `json:"remaining"`
	UsageRatio       float64   `json:"usage_ratio"`
	LastHeaderSeen   string    `json:"last_header_seen,omitempty"`
	LastKnownUpdated time.Time `json:"last_known_updated"`
}

// RecordQuotaFromHeaders updates local quota usage from response headers, with local fallback counting.
func RecordQuotaFromHeaders(path string, headers http.Header) (QuotaSnapshot, error) {
	quotaMu.Lock()
	defer quotaMu.Unlock()

	u, err := loadQuotaUsageUnlocked(path)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	resetIfNewDay(&u)

	// Fallback local counter: count every request when a response is received.
	u.RequestsUsed++

	if headers != nil {
		if remaining, ok := parseQuotaRemaining(headers); ok {
			u.LastKnownRemaining = remaining
			u.LastHeaderSeen = quotaHeaderRemaining
			derivedUsed := u.DailyLimit - remaining
			if derivedUsed > u.RequestsUsed {
				u.RequestsUsed = derivedUsed
			}
			if u.RequestsUsed > u.DailyLimit {
				u.RequestsUsed = u.DailyLimit
			}
		}
	}
	u.LastUpdated = time.Now().UTC()

	if err := saveQuotaUsageUnlocked(path, u); err != nil {
		return QuotaSnapshot{}, err
	}
	return buildQuotaSnapshot(u), nil
}

// LoadQuotaSnapshot reads quota usage from disk and computes current usage ratio.
func LoadQuotaSnapshot(path string) (QuotaSnapshot, error) {
	quotaMu.Lock()
	defer quotaMu.Unlock()

	u, err := loadQuotaUsageUnlocked(path)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	resetIfNewDay(&u)
	if err := saveQuotaUsageUnlocked(path, u); err != nil {
		return QuotaSnapshot{}, err
	}
	return buildQuotaSnapshot(u), nil
}

// ShouldWarnQuota returns true when >=80% of daily quota has been consumed.
func ShouldWarnQuota(s QuotaSnapshot) bool {
	return s.DailyLimit > 0 && s.UsageRatio >= defaultQuotaWarnRatio
}

func loadQuotaUsageUnlocked(path string) (QuotaUsage, error) {
	path = normalizeQuotaPath(path)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			now := time.Now().UTC()
			return QuotaUsage{
				Date:               now.Format("2006-01-02"),
				DailyLimit:         defaultDailyQuotaLimit,
				RequestsUsed:       0,
				LastKnownRemaining: 0,
				LastUpdated:        now,
			}, nil
		}
		return QuotaUsage{}, err
	}
	var u QuotaUsage
	if err := json.Unmarshal(raw, &u); err != nil {
		return QuotaUsage{}, err
	}
	if u.DailyLimit <= 0 {
		u.DailyLimit = defaultDailyQuotaLimit
	}
	if strings.TrimSpace(u.Date) == "" {
		u.Date = time.Now().UTC().Format("2006-01-02")
	}
	return u, nil
}

func saveQuotaUsageUnlocked(path string, u QuotaUsage) error {
	path = normalizeQuotaPath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func normalizeQuotaPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultQuotaUsagePath
	}
	return path
}

func resetIfNewDay(u *QuotaUsage) {
	if u == nil {
		return
	}
	today := time.Now().UTC().Format("2006-01-02")
	if strings.TrimSpace(u.Date) == today {
		return
	}
	u.Date = today
	u.RequestsUsed = 0
	u.LastKnownRemaining = 0
	u.LastHeaderSeen = ""
	u.LastUpdated = time.Now().UTC()
}

func parseQuotaRemaining(h http.Header) (int, bool) {
	v := strings.TrimSpace(h.Get(quotaHeaderRemaining))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func buildQuotaSnapshot(u QuotaUsage) QuotaSnapshot {
	limit := u.DailyLimit
	if limit <= 0 {
		limit = defaultDailyQuotaLimit
	}
	used := u.RequestsUsed
	if used < 0 {
		used = 0
	}
	if used > limit {
		used = limit
	}
	remaining := limit - used
	if u.LastKnownRemaining > 0 && u.LastKnownRemaining < remaining {
		remaining = u.LastKnownRemaining
		used = limit - remaining
	}
	ratio := 0.0
	if limit > 0 {
		ratio = float64(used) / float64(limit)
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return QuotaSnapshot{
		Date:             u.Date,
		DailyLimit:       limit,
		RequestsUsed:     used,
		Remaining:        remaining,
		UsageRatio:       ratio,
		LastHeaderSeen:   u.LastHeaderSeen,
		LastKnownUpdated: u.LastUpdated,
	}
}

// BuildQuotaWarningMessage returns the lead warning line for Mirror output.
func BuildQuotaWarningMessage(s QuotaSnapshot) string {
	return fmt.Sprintf(
		"Sir, we've used %.0f%% of our daily Gemini Pro quota. I'm switching to 'Context Pruning' mode to save tokens for the final build.",
		s.UsageRatio*100,
	)
}
