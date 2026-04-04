package cognition

import (
	"path/filepath"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func TestSentimentTrendAndVolatility(t *testing.T) {
	trend, vol := sentimentTrendAndVolatility([]float64{-0.10, -0.20, -0.45, -0.70})
	if trend >= 0 {
		t.Fatalf("expected negative trend, got %.3f", trend)
	}
	if vol <= 0 {
		t.Fatalf("expected non-zero volatility, got %.3f", vol)
	}
}

func TestBuildReasoningModulationPredictiveIntervention(t *testing.T) {
	t.Setenv("TALOS_PREDICTIVE_INTERVENTION_ENABLED", "true")
	t.Setenv("TALOS_PREDICTIVE_DENSITY_GAIN", "0.35")
	t.Setenv("TALOS_PREDICTIVE_DENSITY_ALERT_AT", "0.20")

	sm, err := state.NewManagerWithPath(filepath.Join(t.TempDir(), "session_state.json"))
	if err != nil {
		t.Fatalf("new state manager: %v", err)
	}
	for _, sample := range []float64{-0.10, -0.25, -0.40, -0.65, -0.80} {
		sm.UpdateDelta(map[string]float64{"mood_score": sample})
	}

	mod := BuildReasoningModulation(sm, "analyze rollout strategy and risks", "deep")
	if mod.EmotionTrend >= 0 {
		t.Fatalf("expected negative emotion trend, got %.3f", mod.EmotionTrend)
	}
	if mod.EmotionVolatility <= 0 {
		t.Fatalf("expected non-zero volatility, got %.3f", mod.EmotionVolatility)
	}
	if mod.PredictiveIntervention == "" {
		t.Fatal("expected predictive intervention to trigger")
	}
}

func TestBuildReasoningModulationPredictiveDisable(t *testing.T) {
	t.Setenv("TALOS_PREDICTIVE_INTERVENTION_ENABLED", "false")
	sm, err := state.NewManagerWithPath(filepath.Join(t.TempDir(), "session_state.json"))
	if err != nil {
		t.Fatalf("new state manager: %v", err)
	}
	for _, sample := range []float64{-0.20, -0.40, -0.70} {
		sm.UpdateDelta(map[string]float64{"mood_score": sample})
	}
	mod := BuildReasoningModulation(sm, "compare options", "balanced")
	if mod.PredictiveIntervention != "" {
		t.Fatalf("expected predictive intervention to be disabled, got %q", mod.PredictiveIntervention)
	}
}
