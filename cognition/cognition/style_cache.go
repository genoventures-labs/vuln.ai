package cognition

import (
	"container/list"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type styleCacheEntry struct {
	key       string
	value     StyleProfile
	expiresAt time.Time
}

type styleProfileCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxItems int
	items    map[string]*list.Element
	lru      *list.List
}

var globalStyleProfileCache = newStyleProfileCache()

func newStyleProfileCache() *styleProfileCache {
	ttlSec := envIntCache("TALOS_STYLE_CACHE_TTL_SEC", 600)
	if ttlSec < 30 {
		ttlSec = 30
	}
	maxItems := envIntCache("TALOS_STYLE_CACHE_MAX", 256)
	if maxItems < 32 {
		maxItems = 32
	}
	return &styleProfileCache{
		ttl:      time.Duration(ttlSec) * time.Second,
		maxItems: maxItems,
		items:    make(map[string]*list.Element),
		lru:      list.New(),
	}
}

func (c *styleProfileCache) get(key string) (StyleProfile, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return StyleProfile{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return StyleProfile{}, false
	}
	entry := el.Value.(styleCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.lru.Remove(el)
		delete(c.items, key)
		return StyleProfile{}, false
	}
	c.lru.MoveToFront(el)
	return entry.value, true
}

func (c *styleProfileCache) set(key string, value StyleProfile) {
	if c == nil || strings.TrimSpace(key) == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value = styleCacheEntry{
			key:       key,
			value:     value,
			expiresAt: time.Now().Add(c.ttl),
		}
		c.lru.MoveToFront(el)
		return
	}
	el := c.lru.PushFront(styleCacheEntry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	})
	c.items[key] = el
	for c.lru.Len() > c.maxItems {
		back := c.lru.Back()
		if back == nil {
			break
		}
		entry := back.Value.(styleCacheEntry)
		delete(c.items, entry.key)
		c.lru.Remove(back)
	}
}

func envIntCache(key string, fallback int) int {
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
