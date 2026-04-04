package cognition

import (
	"container/list"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

type reflectionCacheEntry struct {
	key       string
	value     ReflectionDecision
	expiresAt time.Time
}

type reflectionCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxItems int
	items    map[string]*list.Element
	lru      *list.List
}

var globalReflectionCache = newReflectionCache(DefaultReflectionPolicy(""))

func newReflectionCache(policy ReflectionPolicy) *reflectionCache {
	ttl := policy.CacheTTL
	if ttl <= 0 {
		ttl = 90 * time.Second
	}
	maxItems := policy.CacheMax
	if maxItems < 64 {
		maxItems = 64
	}
	return &reflectionCache{ttl: ttl, maxItems: maxItems, items: map[string]*list.Element{}, lru: list.New()}
}

func (c *reflectionCache) get(key string) (ReflectionDecision, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return ReflectionDecision{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return ReflectionDecision{}, false
	}
	entry := el.Value.(reflectionCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.lru.Remove(el)
		delete(c.items, key)
		return ReflectionDecision{}, false
	}
	c.lru.MoveToFront(el)
	return entry.value, true
}

func (c *reflectionCache) set(key string, value ReflectionDecision) {
	if c == nil || strings.TrimSpace(key) == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value = reflectionCacheEntry{key: key, value: value, expiresAt: time.Now().Add(c.ttl)}
		c.lru.MoveToFront(el)
		return
	}
	el := c.lru.PushFront(reflectionCacheEntry{key: key, value: value, expiresAt: time.Now().Add(c.ttl)})
	c.items[key] = el
	for c.lru.Len() > c.maxItems {
		back := c.lru.Back()
		if back == nil {
			break
		}
		entry := back.Value.(reflectionCacheEntry)
		delete(c.items, entry.key)
		c.lru.Remove(back)
	}
}

func reflectionCacheKey(in ReflectionInput, policy ReflectionPolicy) string {
	raw := strings.Join([]string{
		string(in.Stage),
		strings.ToLower(strings.TrimSpace(in.Query)),
		strings.ToLower(strings.TrimSpace(in.Goal)),
		strings.ToLower(strings.TrimSpace(in.Candidate)),
		strings.Join(normalizeSliceStrings(in.SourceRefs), "|"),
		strings.Join(normalizeSliceStrings(in.ContextFacts), "|"),
		strings.Join(normalizeSliceStrings(in.ShellFacts), "|"),
		policy.EnforcementMode,
		fmt.Sprintf("%.3f|%.3f|%.3f", policy.WarnThreshold, policy.SteerThreshold, policy.VetoThreshold),
	}, "||")
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}
