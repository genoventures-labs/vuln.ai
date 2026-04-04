package cognition

import (
	"container/list"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type supervisionCacheEntry struct {
	key       string
	value     SupervisionDecision
	expiresAt time.Time
}

type supervisionCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxItems int
	items    map[string]*list.Element
	lru      *list.List
}

var globalSupervisionCache = newSupervisionCache()

func newSupervisionCache() *supervisionCache {
	ttlSec := envIntSupervisionCache("TALOS_SYMBOLIC_CACHE_TTL_SEC", 600)
	if ttlSec < 30 {
		ttlSec = 30
	}
	maxItems := envIntSupervisionCache("TALOS_SYMBOLIC_CACHE_MAX", 512)
	if maxItems < 64 {
		maxItems = 64
	}
	return &supervisionCache{
		ttl:      time.Duration(ttlSec) * time.Second,
		maxItems: maxItems,
		items:    map[string]*list.Element{},
		lru:      list.New(),
	}
}

func (c *supervisionCache) get(key string) (SupervisionDecision, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return SupervisionDecision{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return SupervisionDecision{}, false
	}
	entry := el.Value.(supervisionCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.lru.Remove(el)
		delete(c.items, key)
		return SupervisionDecision{}, false
	}
	c.lru.MoveToFront(el)
	return entry.value, true
}

func (c *supervisionCache) set(key string, value SupervisionDecision) {
	if c == nil || strings.TrimSpace(key) == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value = supervisionCacheEntry{
			key:       key,
			value:     value,
			expiresAt: time.Now().Add(c.ttl),
		}
		c.lru.MoveToFront(el)
		return
	}
	el := c.lru.PushFront(supervisionCacheEntry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	})
	c.items[key] = el
	for c.lru.Len() > c.maxItems {
		last := c.lru.Back()
		if last == nil {
			break
		}
		entry := last.Value.(supervisionCacheEntry)
		delete(c.items, entry.key)
		c.lru.Remove(last)
	}
}

func supervisionCacheKey(in SupervisionInput, policy SupervisionPolicy) string {
	raw := strings.Join([]string{
		string(in.Stage),
		strings.ToLower(strings.TrimSpace(in.Query)),
		strings.ToLower(strings.TrimSpace(in.Candidate)),
		strings.Join(normalizeSliceStrings(in.ContextFacts), "|"),
		policy.EnforcementMode,
	}, "||")
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func normalizeSliceStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		s := strings.TrimSpace(strings.ToLower(v))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func envIntSupervisionCache(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}
