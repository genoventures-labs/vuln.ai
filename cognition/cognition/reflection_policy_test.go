package cognition

import "testing"

func TestDefaultReflectionPolicyUsesEnvAndNormalizes(t *testing.T) {
	t.Setenv(reflectionV2EnabledEnv, "true")
	t.Setenv(reflectionModeEnv, "hard")
	t.Setenv(reflectionWarnThresholdEnv, "0.40")
	t.Setenv(reflectionSteerThresholdEnv, "0.30")
	t.Setenv(reflectionVetoThresholdEnv, "0.20")
	t.Setenv(reflectionTimeoutMSEnv, "300")
	t.Setenv(reflectionCitationGateEnv, "true")
	t.Setenv(reflectionCacheTTLEnv, "45")
	t.Setenv(reflectionCacheMaxEnv, "100")

	p := DefaultReflectionPolicy("")
	if !p.Enabled {
		t.Fatalf("expected policy enabled")
	}
	if p.EnforcementMode != "hard" {
		t.Fatalf("expected hard mode, got %q", p.EnforcementMode)
	}
	if p.SteerThreshold < p.WarnThreshold {
		t.Fatalf("expected steer>=warn, got warn=%.2f steer=%.2f", p.WarnThreshold, p.SteerThreshold)
	}
	if p.VetoThreshold < p.SteerThreshold {
		t.Fatalf("expected veto>=steer, got steer=%.2f veto=%.2f", p.SteerThreshold, p.VetoThreshold)
	}
	if p.Timeout.Milliseconds() != 300 {
		t.Fatalf("expected timeout 300ms, got %d", p.Timeout.Milliseconds())
	}
	if !p.CitationGate {
		t.Fatalf("expected citation gate on")
	}
}
