package cog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEnginePriorityAndCheckpoint(t *testing.T) {
	checkpointPath := filepath.Join(t.TempDir(), "cog_checkpoint.json")

	registry, err := NewDecisionRegistry(0.70)
	if err != nil {
		t.Fatalf("new decision registry: %v", err)
	}

	substrate, err := NewCognitiveSubstrate(128, checkpointPath, 5*time.Minute)
	if err != nil {
		t.Fatalf("new cognitive substrate: %v", err)
	}

	engine, err := NewEngine(registry, substrate, 2)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer engine.Stop()

	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start engine: %v", err)
	}

	submitErrCh := make(chan error, 50)
	startA := make(chan struct{})
	startB := make(chan struct{})
	var floodWG sync.WaitGroup

	for i := 0; i < 50; i++ {
		floodWG.Add(1)
		idx := i
		go func() {
			defer floodWG.Done()
			if idx < 25 {
				<-startA
			} else {
				<-startB
			}
			raw := RawTelemetry{
				ID:              fmt.Sprintf("research-%02d", idx),
				Payload:         fmt.Sprintf("Low Priority research thought %d", idx),
				SourceType:      SourceTypeFile,
				SourceRef:       fmt.Sprintf("/tmp/research-%02d.txt", idx),
				ConfidenceScore: 0.40,
				ObservedAt:      time.Now().UTC(),
				MissionTag:      "research",
				AllowOffload:    true,
			}
			if _, submitErr := engine.Submit(raw); submitErr != nil {
				submitErrCh <- fmt.Errorf("submit research thought %d: %w", idx, submitErr)
				return
			}
			submitErrCh <- nil
		}()
	}

	// Flood phase A: release first half concurrently.
	close(startA)

	// Interrupt: mission-critical Sasswall thought.
	criticalID := "mission-critical-sasswall"
	criticalRaw := RawTelemetry{
		ID:              criticalID,
		Payload:         "Mission Critical Sasswall incident",
		SourceType:      SourceTypeURL,
		SourceRef:       "https://sentinel.local/sasswall",
		ConfidenceScore: 0.99,
		ObservedAt:      time.Now().UTC(),
		MissionTag:      "sasswall",
		AllowOffload:    false,
	}
	if _, err := engine.Submit(criticalRaw); err != nil {
		t.Fatalf("submit mission critical thought: %v", err)
	}

	// Flood phase B: release second half concurrently.
	close(startB)
	floodWG.Wait()
	close(submitErrCh)
	for submitErr := range submitErrCh {
		if submitErr != nil {
			t.Fatal(submitErr)
		}
	}

	const expectedTotal = 51
	if err := waitForEpisodicCount(substrate, expectedTotal, 5*time.Second); err != nil {
		t.Fatalf("wait for processing completion: %v", err)
	}

	snapshot := substrate.Snapshot()
	if len(snapshot.EpisodicMemory) != expectedTotal {
		t.Fatalf("expected %d episodic entries, got %d", expectedTotal, len(snapshot.EpisodicMemory))
	}

	criticalIndex := -1
	researchBefore := 0
	for i, entry := range snapshot.EpisodicMemory {
		if entry.ThoughtID == criticalID {
			criticalIndex = i
			break
		}
		researchBefore++
	}
	if criticalIndex < 0 {
		t.Fatalf("mission critical thought %q was not processed", criticalID)
	}

	// Must process critical thought before at least 90%% of research thoughts.
	if researchBefore > 5 {
		t.Fatalf("priority heap violation: %d research thoughts processed before mission critical thought", researchBefore)
	}

	if err := substrate.writeCheckpoint(); err != nil {
		t.Fatalf("manual checkpoint: %v", err)
	}

	checkpointData, err := os.ReadFile(checkpointPath)
	if err != nil {
		t.Fatalf("read checkpoint file: %v", err)
	}

	var checkpointSnapshot SubstrateSnapshot
	if err := json.Unmarshal(checkpointData, &checkpointSnapshot); err != nil {
		t.Fatalf("decode checkpoint json: %v", err)
	}

	if len(checkpointSnapshot.ActiveShards) != len(snapshot.ActiveShards) {
		t.Fatalf("active shards mismatch: checkpoint=%d snapshot=%d", len(checkpointSnapshot.ActiveShards), len(snapshot.ActiveShards))
	}
	if len(checkpointSnapshot.EpisodicMemory) != expectedTotal {
		t.Fatalf("checkpoint episodic entries mismatch: expected %d, got %d", expectedTotal, len(checkpointSnapshot.EpisodicMemory))
	}
}

func waitForEpisodicCount(substrate *CognitiveSubstrate, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot := substrate.Snapshot()
		if len(snapshot.EpisodicMemory) >= want {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := substrate.Snapshot()
	return fmt.Errorf("timeout waiting for episodic entries: want=%d got=%d", want, len(snapshot.EpisodicMemory))
}
