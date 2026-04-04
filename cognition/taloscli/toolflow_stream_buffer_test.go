package taloscli

import (
	"strings"
	"testing"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/toolflow"
)

func TestCollaborativeStreamHubBroadcastAndWatchers(t *testing.T) {
	hub := newCollaborativeStreamHub()
	ch, unsub := hub.subscribe("verifier", 2)
	resultCh := watchCollaborativeStream("verifier", ch, 2)

	hub.broadcast(toolflow.ExecEvent{Tool: "web_search", Kind: "output", Chunk: "normal progress", At: time.Now()})
	hub.broadcast(toolflow.ExecEvent{Tool: "fetch_url", Kind: "error", Chunk: "smoking gun: exposed token", At: time.Now()})
	unsub()

	summary, ok := <-resultCh
	if !ok {
		t.Fatal("expected watcher summary")
	}
	if summary.Watcher != "verifier" {
		t.Fatalf("unexpected watcher id: %s", summary.Watcher)
	}
	if summary.Frames < 2 {
		t.Fatalf("expected >=2 frames, got %d", summary.Frames)
	}
	if summary.Errors < 1 {
		t.Fatalf("expected >=1 error frame, got %d", summary.Errors)
	}
	if len(summary.Highlights) == 0 {
		t.Fatalf("expected highlight detection, got %+v", summary)
	}
}

func TestFormatCollaborativeStreamStatus(t *testing.T) {
	status := formatCollaborativeStreamStatus([]collaborativeStreamWatcherSummary{
		{Watcher: "planner", Frames: 3, Errors: 0, Highlights: []string{"h1"}, Dropped: 1},
		{Watcher: "verifier", Frames: 4, Errors: 1, Dropped: 0},
	})
	if !strings.Contains(status, "COLLAB_STREAM: active_watchers=2") {
		t.Fatalf("unexpected status header: %s", status)
	}
	if !strings.Contains(status, "COLLAB_SHARD[planner]") {
		t.Fatalf("missing planner shard line: %s", status)
	}
}
