package memory

import (
	"container/list"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

type marCacheEntry struct {
	key       string
	value     AnchoredContext
	expiresAt time.Time
}

type marContextCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxItems int
	items    map[string]*list.Element
	lru      *list.List
}

func newMARContextCache() *marContextCache {
	ttlSec := envIntWithFloor(marCacheTTLEnv, 180, 10)
	maxItems := envIntWithFloor(marCacheMaxEnv, 512, 64)
	return &marContextCache{
		ttl:      time.Duration(ttlSec) * time.Second,
		maxItems: maxItems,
		items:    map[string]*list.Element{},
		lru:      list.New(),
	}
}

func (c *marContextCache) get(key string) (AnchoredContext, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return AnchoredContext{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return AnchoredContext{}, false
	}
	entry := el.Value.(marCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.lru.Remove(el)
		delete(c.items, key)
		return AnchoredContext{}, false
	}
	c.lru.MoveToFront(el)
	return entry.value, true
}

func (c *marContextCache) set(key string, value AnchoredContext) {
	if c == nil || strings.TrimSpace(key) == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value = marCacheEntry{key: key, value: value, expiresAt: time.Now().Add(c.ttl)}
		c.lru.MoveToFront(el)
		return
	}
	el := c.lru.PushFront(marCacheEntry{key: key, value: value, expiresAt: time.Now().Add(c.ttl)})
	c.items[key] = el
	for c.lru.Len() > c.maxItems {
		back := c.lru.Back()
		if back == nil {
			break
		}
		entry := back.Value.(marCacheEntry)
		delete(c.items, entry.key)
		c.lru.Remove(back)
	}
}

func marCacheKey(namespace string, query string, historyK int, knowledgeK int, policy MARPolicy) string {
	raw := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(namespace)),
		strings.ToLower(strings.TrimSpace(query)),
		fmt.Sprintf("h=%d", historyK),
		fmt.Sprintf("k=%d", knowledgeK),
		fmt.Sprintf("p=%t", policy.Enabled),
		fmt.Sprintf("c=%d", policy.CandidateLimit),
		fmt.Sprintf("m=%d", policy.MaxAnchors),
		fmt.Sprintf("s=%.4f", policy.MinAnchorScore),
		fmt.Sprintf("tb=%.4f", policy.TopologyBoost),
		fmt.Sprintf("w=%.4f,%.4f,%.4f,%.4f,%.4f", policy.WeightSemantic, policy.WeightLexical, policy.WeightImportance, policy.WeightFreshness, policy.WeightTopology),
	}, "||")
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}
