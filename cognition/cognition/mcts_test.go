package cognition

import (
	"context"
	"strings"
	"testing"
)

func TestSearchV2PrefersHigherConfidenceCandidate(t *testing.T) {
	e := MCTSEngine{
		Config: MCTSConfig{
			Iterations:         6,
			BranchFactor:       3,
			RolloutDepth:       2,
			Strategy:           MCTSStrategyPUCT,
			MaxChildrenPerNode: 3,
			MaxConcurrency:     2,
			Deterministic:      true,
			Seed:               11,
		},
		Callbacks: MCTSCallbacks{
			ProposeBranches: func(_ context.Context, _ string, _ int) ([]string, error) {
				return []string{"candidate-low", "candidate-best", "candidate-mid"}, nil
			},
			EvaluatePath: func(_ context.Context, candidate string) (MCTSEvaluation, error) {
				score := 0.2
				if strings.Contains(candidate, "mid") {
					score = 0.55
				}
				if strings.Contains(candidate, "best") {
					score = 0.9
				}
				return MCTSEvaluation{Confidence: score, Candidate: candidate, Prior: score}, nil
			},
			AdversarialEval: func(_ context.Context, candidate string) (MCTSEvaluation, error) {
				score := 0.2
				if strings.Contains(candidate, "mid") {
					score = 0.5
				}
				if strings.Contains(candidate, "best") {
					score = 0.85
				}
				return MCTSEvaluation{Confidence: score, Candidate: candidate, Prior: score}, nil
			},
		},
	}

	res, err := e.SearchV2(context.Background(), "draft")
	if err != nil {
		t.Fatalf("search v2 failed: %v", err)
	}
	if !strings.Contains(res.BestAnswer, "best") {
		t.Fatalf("expected best candidate, got %q", res.BestAnswer)
	}
	if res.Strategy != string(MCTSStrategyPUCT) {
		t.Fatalf("expected puct strategy, got %q", res.Strategy)
	}
	if res.IterationsRun == 0 {
		t.Fatalf("expected non-zero iterations")
	}
}

func TestSearchCompatibilityWrapper(t *testing.T) {
	e := MCTSEngine{
		Config: MCTSConfig{Iterations: 2, BranchFactor: 2, RolloutDepth: 1, Strategy: MCTSStrategyUCB1, Deterministic: true, Seed: 1},
		Callbacks: MCTSCallbacks{
			ProposeBranches: func(_ context.Context, _ string, _ int) ([]string, error) { return []string{"a", "b"}, nil },
			EvaluatePath: func(_ context.Context, c string) (MCTSEvaluation, error) {
				return MCTSEvaluation{Confidence: map[string]float64{"a": 0.2, "b": 0.8}[c], Candidate: c}, nil
			},
			AdversarialEval: func(_ context.Context, c string) (MCTSEvaluation, error) {
				return MCTSEvaluation{Confidence: map[string]float64{"a": 0.2, "b": 0.7}[c], Candidate: c}, nil
			},
		},
	}
	best, ok, root, err := e.Search(context.Background(), "draft")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !ok || strings.TrimSpace(best) == "" {
		t.Fatalf("expected non-empty winning answer")
	}
	if root == nil {
		t.Fatalf("expected root node")
	}
}

func TestProgressiveWideningRespectsMaxChildren(t *testing.T) {
	e := MCTSEngine{
		Config: MCTSConfig{BranchFactor: 6, MaxChildrenPerNode: 2, WideningAlpha: 0.8, WideningK: 4, Strategy: MCTSStrategyPUCT},
		Callbacks: MCTSCallbacks{
			ProposeBranches: func(_ context.Context, _ string, _ int) ([]string, error) {
				return []string{"one", "two", "three", "four"}, nil
			},
			EvaluatePath: func(_ context.Context, c string) (MCTSEvaluation, error) {
				return MCTSEvaluation{Confidence: 0.5, Candidate: c}, nil
			},
			AdversarialEval: func(_ context.Context, c string) (MCTSEvaluation, error) {
				return MCTSEvaluation{Confidence: 0.5, Candidate: c}, nil
			},
		},
	}
	cfg := e.normalizedConfig()
	node := &ThoughtNode{ID: "root", Answer: "seed", Visits: 100}
	for i := 0; i < 10; i++ {
		if !shouldExpand(node, cfg) {
			break
		}
		_ = e.expandOneChild(context.Background(), node, cfg)
	}
	if len(node.Children) > cfg.MaxChildrenPerNode {
		t.Fatalf("expected <= %d children, got %d", cfg.MaxChildrenPerNode, len(node.Children))
	}
}
