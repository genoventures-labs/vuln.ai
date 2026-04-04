package connectors

import (
	"fmt"
	"sync"
)

var (
	mu       sync.RWMutex
	registry = map[string]Connector{}
)

// Register adds a connector to the global registry. Panics on duplicate name.
func Register(c Connector) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[c.Name()]; ok {
		panic(fmt.Sprintf("connector already registered: %s", c.Name()))
	}
	registry[c.Name()] = c
}

// Get returns a connector by name, or nil if not found.
func Get(name string) (Connector, bool) {
	mu.RLock()
	defer mu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

// All returns all registered connectors.
func All() []Connector {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Connector, 0, len(registry))
	for _, c := range registry {
		out = append(out, c)
	}
	return out
}
