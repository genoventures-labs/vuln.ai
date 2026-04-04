package skills

import "testing"

func TestRouteSkillPicksBestCandidate(t *testing.T) {
	t.Setenv("TALOS_SKILLS_ROUTER_MIN_CONFIDENCE", "0.20")
	reg := NewSkillRegistry(t.TempDir())
	_ = reg.Upsert(SkillRecord{SkillID: "s1", RevisionID: "r1", Version: "0.1.0", Status: SkillStatusActive, Active: true, Enabled: true, Name: "jira_helper", Intent: "summarize jira ticket", TaskType: "general", RootDir: "x", SourcePath: "x/a.go", ManifestPath: "x/m.json"})
	_ = reg.Upsert(SkillRecord{SkillID: "s2", RevisionID: "r1", Version: "0.1.0", Status: SkillStatusActive, Active: true, Enabled: true, Name: "k8s_helper", Intent: "analyze kubernetes deployment", TaskType: "infra", RootDir: "y", SourcePath: "y/a.go", ManifestPath: "y/m.json"})
	d, err := RouteSkill(reg, RouteRequest{Query: "summarize jira status for ticket", TaskType: "general"})
	if err != nil {
		t.Fatalf("route skill: %v", err)
	}
	if d.Chosen == nil || d.Chosen.SkillID != "s1" {
		t.Fatalf("expected s1 chosen, got %+v", d.Chosen)
	}
}
