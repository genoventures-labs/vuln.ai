package mesh

import (
	"context"
	"strings"
	"sync"
)

// StaticNeighborStore keeps an in-memory roster, useful for process-local mesh bootstrap.
type StaticNeighborStore struct {
	mu        sync.RWMutex
	neighbors []Neighbor
	inactive  map[int64]bool
}

func NewStaticNeighborStore(neighbors []Neighbor) *StaticNeighborStore {
	cp := make([]Neighbor, 0, len(neighbors))
	for _, n := range neighbors {
		if strings.TrimSpace(n.URL) == "" {
			continue
		}
		cp = append(cp, n)
	}
	return &StaticNeighborStore{
		neighbors: cp,
		inactive:  make(map[int64]bool),
	}
}

func (s *StaticNeighborStore) ListActiveNeighbors(_ context.Context) ([]Neighbor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Neighbor, 0, len(s.neighbors))
	for _, n := range s.neighbors {
		if s.inactive[n.ID] {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *StaticNeighborStore) MarkInactive(_ context.Context, id int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inactive[id] = true
	return nil
}

// MemoryToolStore is an in-memory ToolStore for daemon-local resolver wiring.
type MemoryToolStore struct {
	mu    sync.RWMutex
	tools map[string]ToolDescriptor
}

func NewMemoryToolStore() *MemoryToolStore {
	return &MemoryToolStore{tools: make(map[string]ToolDescriptor)}
}

func (s *MemoryToolStore) FindVerified(_ context.Context, requirement ToolRequirement) (*ToolDescriptor, error) {
	name := strings.TrimSpace(requirement.Name)
	if name == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tool, ok := s.tools[name]
	if !ok || !tool.Verified {
		return nil, nil
	}
	cp := tool
	return &cp, nil
}

func (s *MemoryToolStore) Upsert(_ context.Context, tool ToolDescriptor) error {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[name] = tool
	return nil
}

// MemoryThoughtShardStore is an in-memory ThoughtShardStore.
type MemoryThoughtShardStore struct {
	mu     sync.RWMutex
	shards []CognitiveShard
}

func NewMemoryThoughtShardStore() *MemoryThoughtShardStore {
	return &MemoryThoughtShardStore{shards: make([]CognitiveShard, 0, 16)}
}

func (s *MemoryThoughtShardStore) SaveLocalShard(_ context.Context, shard CognitiveShard) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shards = append(s.shards, shard)
	return nil
}

func (s *MemoryThoughtShardStore) CountLocalShards(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.shards), nil
}
