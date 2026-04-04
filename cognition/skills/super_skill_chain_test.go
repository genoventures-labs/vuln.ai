package skills

import "testing"

func TestDraftTemporarySuperSkillRequiresThree(t *testing.T) {
	_, _, err := DraftTemporarySuperSkill("complex task", []SkillRecord{{SkillID: "s1"}, {SkillID: "s2"}})
	if err == nil {
		t.Fatalf("expected error for less than 3 candidates")
	}
}

func TestDraftTemporarySuperSkillBuildsRecord(t *testing.T) {
	rec, chain, err := DraftTemporarySuperSkill("complex task", []SkillRecord{
		{SkillID: "s1", Name: "alpha", Status: SkillStatusActive},
		{SkillID: "s2", Name: "beta", Status: SkillStatusActive},
		{SkillID: "s3", Name: "gamma", Status: SkillStatusActive},
	})
	if err != nil {
		t.Fatalf("draft failed: %v", err)
	}
	if rec.SkillID == "" || rec.RevisionID == "" || rec.Name == "" {
		t.Fatalf("unexpected drafted record: %+v", rec)
	}
	if len(chain) != 3 {
		t.Fatalf("expected chain length 3, got %d", len(chain))
	}
}

func TestExecuteWithPolicySuperSkillChain(t *testing.T) {
	super := SkillRecord{SkillID: "super_1", RevisionID: "rev_1", Status: SkillStatusValidated, Name: "super"}
	chain := []SkillRecord{
		{SkillID: "s1", RevisionID: "r1", Status: SkillStatusActive, Name: "a", Intent: "one"},
		{SkillID: "s2", RevisionID: "r1", Status: SkillStatusActive, Name: "b", Intent: "two"},
		{SkillID: "s3", RevisionID: "r1", Status: SkillStatusActive, Name: "c", Intent: "three"},
	}
	res, err := ExecuteWithPolicy(nil, ExecutionRequest{Skill: super, Query: "complex", Chain: chain, Input: map[string]any{"query": "complex"}})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Status != "ok" {
		t.Fatalf("expected ok, got %s", res.Status)
	}
	if v, ok := res.Output["super_skill"].(bool); !ok || !v {
		t.Fatalf("expected super_skill marker in output: %+v", res.Output)
	}
	if v, ok := res.Output["chain_count"].(int); ok {
		if v != 3 {
			t.Fatalf("expected chain_count=3, got %d", v)
		}
	} else {
		// JSON-like numbers can decode as float64 in some paths; accept that here.
		fv, okf := res.Output["chain_count"].(float64)
		if !okf || int(fv) != 3 {
			t.Fatalf("expected chain_count=3, got %v", res.Output["chain_count"])
		}
	}
}
