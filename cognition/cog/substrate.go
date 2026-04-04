package cog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	defaultCheckpointPath = ".memory/cog_checkpoint.json"
	defaultCheckpointTick = 10 * time.Second
)

// EpisodicEntry stores a prior reasoning outcome/session artifact.
type EpisodicEntry struct {
	Timestamp time.Time     `json:"timestamp"`
	ThoughtID string        `json:"thought_id"`
	Route     RouteDecision `json:"route"`
	Summary   string        `json:"summary"`
}

// ProceduralSkill tracks capability learned from talos learn.
type ProceduralSkill struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Source    string    `json:"source"`
	LearnedAt time.Time `json:"learned_at"`
}

// SubstrateSnapshot is the persisted stream-of-consciousness checkpoint.
type SubstrateSnapshot struct {
	UpdatedAt        time.Time                  `json:"updated_at"`
	Stream           []ThoughtObject            `json:"stream"`
	EpisodicMemory   []EpisodicEntry            `json:"episodic_memory"`
	ProceduralMemory map[string]ProceduralSkill `json:"procedural_memory"`
	ActiveShards     []string                   `json:"active_shards"`
	CurrentLoad      float64                    `json:"current_load"`
}

type routedThought struct {
	Thought ThoughtObject
	Route   RouteDecision
}

// CognitiveSubstrate maintains stream-of-consciousness state with checkpointing.
type CognitiveSubstrate struct {
	mu sync.RWMutex

	stream           []ThoughtObject
	episodicMemory   []EpisodicEntry
	proceduralMemory map[string]ProceduralSkill
	activeShards     map[string]struct{}
	currentLoad      float64

	ingestCh chan routedThought
	stopCh   chan struct{}
	wg       sync.WaitGroup

	checkpointPath string
	checkpointTick time.Duration
}

func NewCognitiveSubstrate(bufferSize int, checkpointPath string, checkpointTick time.Duration) (*CognitiveSubstrate, error) {
	if bufferSize <= 0 {
		return nil, fmt.Errorf("cognitive substrate: buffer size must be positive")
	}
	if checkpointPath == "" {
		checkpointPath = defaultCheckpointPath
	}
	if checkpointTick <= 0 {
		checkpointTick = defaultCheckpointTick
	}
	return &CognitiveSubstrate{
		stream:           make([]ThoughtObject, 0, bufferSize),
		episodicMemory:   make([]EpisodicEntry, 0, bufferSize),
		proceduralMemory: make(map[string]ProceduralSkill),
		activeShards:     make(map[string]struct{}),
		ingestCh:         make(chan routedThought, bufferSize),
		stopCh:           make(chan struct{}),
		checkpointPath:   checkpointPath,
		checkpointTick:   checkpointTick,
	}, nil
}

func (s *CognitiveSubstrate) Start(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("cognitive substrate: nil receiver")
	}
	s.wg.Add(1)
	go s.loop(ctx)
	return nil
}

func (s *CognitiveSubstrate) Stop() {
	if s == nil {
		return
	}
	close(s.stopCh)
	s.wg.Wait()
}

func (s *CognitiveSubstrate) Ingest(thought ThoughtObject, route RouteDecision) error {
	if s == nil {
		return fmt.Errorf("cognitive substrate: nil receiver")
	}
	if thought.ID == "" {
		return fmt.Errorf("cognitive substrate: thought id is empty")
	}
	select {
	case s.ingestCh <- routedThought{Thought: thought, Route: route}:
		return nil
	default:
		return fmt.Errorf("cognitive substrate: ingest channel is full")
	}
}

func (s *CognitiveSubstrate) SetCurrentLoad(load float64) error {
	if s == nil {
		return fmt.Errorf("cognitive substrate: nil receiver")
	}
	if load < 0 || load > 1 {
		return fmt.Errorf("cognitive substrate: current load must be in [0,1]")
	}
	s.mu.Lock()
	s.currentLoad = load
	s.mu.Unlock()
	return nil
}

func (s *CognitiveSubstrate) AddProceduralSkill(skill ProceduralSkill) error {
	if s == nil {
		return fmt.Errorf("cognitive substrate: nil receiver")
	}
	if skill.Name == "" {
		return fmt.Errorf("cognitive substrate: procedural skill name is empty")
	}
	if skill.LearnedAt.IsZero() {
		skill.LearnedAt = time.Now().UTC()
	}
	s.mu.Lock()
	s.proceduralMemory[skill.Name] = skill
	s.mu.Unlock()
	return nil
}

func (s *CognitiveSubstrate) Snapshot() SubstrateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	shards := make([]string, 0, len(s.activeShards))
	for id := range s.activeShards {
		shards = append(shards, id)
	}
	sort.Strings(shards)

	streamCopy := append([]ThoughtObject(nil), s.stream...)
	episodicCopy := append([]EpisodicEntry(nil), s.episodicMemory...)
	proceduralCopy := make(map[string]ProceduralSkill, len(s.proceduralMemory))
	for k, v := range s.proceduralMemory {
		proceduralCopy[k] = v
	}

	return SubstrateSnapshot{
		UpdatedAt:        time.Now().UTC(),
		Stream:           streamCopy,
		EpisodicMemory:   episodicCopy,
		ProceduralMemory: proceduralCopy,
		ActiveShards:     shards,
		CurrentLoad:      s.currentLoad,
	}
}

func (s *CognitiveSubstrate) loop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.checkpointTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = s.writeCheckpoint()
			return
		case <-s.stopCh:
			_ = s.writeCheckpoint()
			return
		case msg := <-s.ingestCh:
			s.applyRoutedThought(msg)
		case <-ticker.C:
			_ = s.writeCheckpoint()
		}
	}
}

func (s *CognitiveSubstrate) applyRoutedThought(msg routedThought) {
	now := time.Now().UTC()

	s.mu.Lock()
	s.stream = append(s.stream, msg.Thought)
	s.activeShards[msg.Thought.ID] = struct{}{}
	s.episodicMemory = append(s.episodicMemory, EpisodicEntry{
		Timestamp: now,
		ThoughtID: msg.Thought.ID,
		Route:     msg.Route,
		Summary:   msg.Thought.Content,
	})
	delete(s.activeShards, msg.Thought.ID)
	s.mu.Unlock()
}

func (s *CognitiveSubstrate) writeCheckpoint() error {
	snapshot := s.Snapshot()
	if err := os.MkdirAll(filepath.Dir(s.checkpointPath), 0o755); err != nil {
		return fmt.Errorf("cognitive substrate: create checkpoint dir: %w", err)
	}
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("cognitive substrate: marshal checkpoint: %w", err)
	}
	if err := os.WriteFile(s.checkpointPath, payload, 0o644); err != nil {
		return fmt.Errorf("cognitive substrate: write checkpoint: %w", err)
	}
	return nil
}
