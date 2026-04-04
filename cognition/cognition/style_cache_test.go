package cognition

import (
	"container/list"
	"testing"
	"time"
)

func TestStyleProfileCache_SetGetAndExpiry(t *testing.T) {
	c := &styleProfileCache{
		ttl:      20 * time.Millisecond,
		maxItems: 8,
		items:    map[string]*list.Element{},
		lru:      list.New(),
	}
	c.set("k1", StyleProfile{Tone: "direct_technical"})
	if _, ok := c.get("k1"); !ok {
		t.Fatal("expected cache hit")
	}
	time.Sleep(30 * time.Millisecond)
	if _, ok := c.get("k1"); ok {
		t.Fatal("expected cache expiry")
	}
}
