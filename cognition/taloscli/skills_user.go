package taloscli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
	"github.com/spf13/cobra"
)

var userSkillName string
var userSkillIntent string
var userSkillDescription string
var userSkillReasoningTier string
var userSkillTaskType string
var userSkillRequestedTools []string
var userSkillRequestedDomains []string
var userSkillShowID string
var userSkillRevisionID string
var userSkillExportOut string
var userSkillImportIn string
var userSkillMigrateApply bool

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Create and inspect user-defined TALOS skills.",
}

var skillsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a persistent TALOS self-skill (requires preflight allow).",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(userSkillName) == "" {
			fmt.Println("Error: --name is required")
			return
		}
		intent := resolvedSkillIntent(userSkillIntent, userSkillDescription)
		if intent == "" {
			fmt.Println("Error: --intent is required (or use --description)")
			return
		}
		tc, err := tools.NewGLMToolClient()
		if err != nil {
			fmt.Printf("Error initializing tool client: %v\n", err)
			return
		}
		creator := skills.NewUserSkillCreator(tc)
		reqTools, err := parseRequestedTools(userSkillRequestedTools)
		if err != nil {
			fmt.Printf("Error parsing --requested-tool: %v\n", err)
			return
		}
		res, err := creator.Create(context.Background(), skills.UserSkillCreateRequest{
			Name:             strings.TrimSpace(userSkillName),
			Intent:           intent,
			Description:      strings.TrimSpace(userSkillDescription),
			ReasoningTier:    strings.TrimSpace(userSkillReasoningTier),
			TaskType:         strings.TrimSpace(userSkillTaskType),
			Namespace:        activeRuntimeNamespace(),
			RequestedTools:   reqTools,
			RequestedDomains: dedupeSkillStrings(userSkillRequestedDomains),
		})
		if err != nil {
			fmt.Printf("Error creating user skill: %v\n", err)
			return
		}
		if ns := activeRuntimeNamespace(); ns != "" {
			store, serr := namespaceCapabilityStoreFactory()
			if serr != nil {
				fmt.Printf("Warning: failed to load namespace capability policy: %v\n", serr)
			} else {
				store.AllowSkill(ns, strings.TrimSpace(res.Artifact.SkillID))
				if serr = store.Save(); serr != nil {
					fmt.Printf("Warning: failed to persist namespace capability policy: %v\n", serr)
				}
			}
		}
		fmt.Println("User skill created.")
		fmt.Printf("skill_id=%s\n", strings.TrimSpace(res.Artifact.SkillID))
		fmt.Printf("root_dir=%s\n", strings.TrimSpace(res.Artifact.RootDir))
		fmt.Printf("source_path=%s\n", strings.TrimSpace(res.Artifact.SourcePath))
		fmt.Printf("manifest_path=%s\n", strings.TrimSpace(res.Artifact.ManifestPath))
		if res.Artifact.CompileOK {
			fmt.Println("compile_ok=true")
		} else {
			fmt.Println("compile_ok=false")
		}
		fmt.Printf("preflight_decision=%s\n", strings.TrimSpace(res.Preflight.Decision))
	},
}

var skillsPreflightCmd = &cobra.Command{
	Use:   "preflight",
	Short: "Run `/skills/preflight` for a proposed user skill.",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(userSkillName) == "" {
			fmt.Println("Error: --name is required")
			return
		}
		intent := resolvedSkillIntent(userSkillIntent, userSkillDescription)
		if intent == "" {
			fmt.Println("Error: --intent is required (or use --description)")
			return
		}
		tc, err := tools.NewGLMToolClient()
		if err != nil {
			fmt.Printf("Error initializing tool client: %v\n", err)
			return
		}
		reqTools, err := parseRequestedTools(userSkillRequestedTools)
		if err != nil {
			fmt.Printf("Error parsing --requested-tool: %v\n", err)
			return
		}
		resp, err := tc.SkillPreflight(tools.SkillPreflightRequest{
			SkillName:        strings.TrimSpace(userSkillName),
			Intent:           intent,
			RequestedTools:   reqTools,
			RequestedDomains: dedupeSkillStrings(userSkillRequestedDomains),
		})
		if err != nil {
			fmt.Printf("Preflight error: %v\n", err)
			return
		}
		fmt.Printf("decision=%s\n", strings.TrimSpace(resp.Decision))
		fmt.Printf("reason=%s\n", strings.TrimSpace(resp.Reason))
		fmt.Printf("invoke_count=%d\n", len(resp.Invoke))
		for i, inv := range resp.Invoke {
			fmt.Printf("invoke_%d_path=%s\n", i+1, strings.TrimSpace(inv.Path))
			fmt.Printf("invoke_%d_url=%s\n", i+1, strings.TrimSpace(inv.URL))
		}
		if len(resp.Limits) > 0 {
			b, _ := json.Marshal(resp.Limits)
			fmt.Printf("limits=%s\n", string(b))
		}
	},
}

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List user-defined skills in .skills/permanent.",
	Run: func(cmd *cobra.Command, args []string) {
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		activeNS := activeRuntimeNamespace()
		var store *state.NamespaceCapabilityStore
		if activeNS != "" {
			s, err := namespaceCapabilityStoreFactory()
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Error loading namespace capability policy: %v\n", err)
				return
			}
			store = s
		}
		recs, err := reg.ListEnabled()
		if err == nil && activeNS != "" && store != nil {
			filtered := make([]skills.SkillRecord, 0, len(recs))
			for _, rec := range recs {
				if store.IsSkillAllowed(activeNS, strings.TrimSpace(rec.SkillID)) {
					filtered = append(filtered, rec)
				}
			}
			recs = filtered
		}
		if err == nil && len(recs) > 0 {
			sort.SliceStable(recs, func(i, j int) bool {
				return recs[i].UpdatedAt.After(recs[j].UpdatedAt)
			})
			fmt.Fprintf(cmd.OutOrStdout(), "TALOS SKILLS\n\nCOMMAND\n  talos skills list\n\nSTATUS\n  SUCCESS\n\nSUMMARY\n  enabled_skills: %d\n\nSKILLS\n", len(recs))
			for i, rec := range recs {
				name := strings.TrimSpace(rec.Name)
				if name == "" {
					name = "(unnamed skill)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %d. %s\n", i+1, name)
				fmt.Fprintf(cmd.OutOrStdout(), "     id: %s\n", valueOrPlaceholder(rec.SkillID))
				fmt.Fprintf(cmd.OutOrStdout(), "     revision/version: %s / %s\n", valueOrPlaceholder(rec.RevisionID), valueOrPlaceholder(rec.Version))
				fmt.Fprintf(cmd.OutOrStdout(), "     intent: %s\n", valueOrPlaceholder(rec.Intent))
				fmt.Fprintf(cmd.OutOrStdout(), "     tier/task: %s / %s\n", valueOrPlaceholder(rec.ReasoningTier), valueOrPlaceholder(rec.TaskType))
				fmt.Fprintf(cmd.OutOrStdout(), "     status/active: %s / %t\n", valueOrPlaceholder(rec.Status), rec.Active)
				fmt.Fprintf(cmd.OutOrStdout(), "     updated: %s\n", formatSkillTime(rec.UpdatedAt))
				fmt.Fprintf(cmd.OutOrStdout(), "     path: %s\n", valueOrPlaceholder(rec.RootDir))
			}
			return
		}
		paths, err := listSkillManifests(".skills/permanent")
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error listing skills: %v\n", err)
			return
		}
		if activeNS != "" && store != nil {
			filtered := make([]string, 0, len(paths))
			for _, p := range paths {
				id, _ := readSkillIdentity(p)
				if store.IsSkillAllowed(activeNS, strings.TrimSpace(id)) {
					filtered = append(filtered, p)
				}
			}
			paths = filtered
		}
		if len(paths) == 0 {
			if activeNS != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "No skills are bound in active namespace: %s\n", activeNS)
				return
			}
			fmt.Fprintln(cmd.OutOrStdout(), "No user skills found.")
			return
		}
		fmt.Fprintf(cmd.OutOrStdout(), "TALOS SKILLS\n\nCOMMAND\n  talos skills list\n\nSTATUS\n  SUCCESS\n\nSUMMARY\n  enabled_skills: %d\n\nSKILLS\n", len(paths))
		for _, p := range paths {
			id, name := readSkillIdentity(p)
			if strings.TrimSpace(name) == "" {
				name = "(unnamed skill)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", name)
			fmt.Fprintf(cmd.OutOrStdout(), "    id: %s\n", valueOrPlaceholder(id))
			fmt.Fprintf(cmd.OutOrStdout(), "    path: %s\n", valueOrPlaceholder(filepath.Dir(p)))
		}
	},
}

var skillsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show one user skill manifest by skill ID, name, or path suffix.",
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.TrimSpace(userSkillShowID)
		if query == "" {
			fmt.Println("Error: --id is required")
			return
		}
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		recs, err := reg.ListEnabled()
		if err == nil {
			for _, rec := range recs {
				if query == rec.SkillID || query == rec.Name || strings.Contains(filepath.ToSlash(rec.ManifestPath), query) {
					b, rerr := os.ReadFile(strings.TrimSpace(rec.ManifestPath))
					if rerr != nil {
						fmt.Printf("Error reading manifest: %v\n", rerr)
						return
					}
					fmt.Println(string(b))
					return
				}
			}
		}
		paths, err := listSkillManifests(".skills/permanent")
		if err != nil {
			fmt.Printf("Error loading skills: %v\n", err)
			return
		}
		for _, p := range paths {
			id, name := readSkillIdentity(p)
			if query == id || query == name || strings.Contains(filepath.ToSlash(p), query) {
				b, err := os.ReadFile(p)
				if err != nil {
					fmt.Printf("Error reading manifest: %v\n", err)
					return
				}
				fmt.Println(string(b))
				return
			}
		}
		fmt.Printf("Skill not found: %s\n", query)
	},
}

var skillsValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Mark a skill revision as validated.",
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.TrimSpace(userSkillShowID)
		if query == "" {
			fmt.Println("Error: --id is required")
			return
		}
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		rec, err := resolveRequestedSkillSelection(query)
		if err != nil || rec == nil {
			fmt.Printf("Skill not found: %s\n", query)
			return
		}
		recs, err := reg.ListRevisions(rec.SkillID)
		if err != nil {
			fmt.Printf("Error listing revisions: %v\n", err)
			return
		}
		if len(recs) == 0 {
			fmt.Println("No revisions found.")
			return
		}
		target := recs[0]
		if strings.TrimSpace(userSkillRevisionID) != "" {
			for _, r := range recs {
				if strings.EqualFold(strings.TrimSpace(r.RevisionID), strings.TrimSpace(userSkillRevisionID)) {
					target = r
					break
				}
			}
		}
		target.Status = skills.SkillStatusValidated
		target.Enabled = true
		if err := reg.Upsert(target); err != nil {
			fmt.Printf("Error validating skill: %v\n", err)
			return
		}
		fmt.Printf("Validated skill.\nskill_id=%s\nrevision_id=%s\nstatus=%s\n", target.SkillID, target.RevisionID, target.Status)
	},
}

var skillsActivateCmd = &cobra.Command{
	Use:   "activate",
	Short: "Activate one skill revision.",
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.TrimSpace(userSkillShowID)
		if query == "" {
			fmt.Println("Error: --id is required")
			return
		}
		rec, err := resolveRequestedSkillSelection(query)
		if err != nil || rec == nil {
			fmt.Printf("Skill not found: %s\n", query)
			return
		}
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		if err := reg.ActivateRevision(rec.SkillID, strings.TrimSpace(userSkillRevisionID)); err != nil {
			fmt.Printf("Error activating skill revision: %v\n", err)
			return
		}
		active, ok, err := reg.ResolveActive(rec.SkillID)
		if err != nil || !ok || active == nil {
			fmt.Printf("Skill activated but active resolution failed: %v\n", err)
			return
		}
		fmt.Printf("Activated skill.\nskill_id=%s\nrevision_id=%s\nstatus=%s\n", active.SkillID, active.RevisionID, active.Status)
	},
}

var skillsDeprecateCmd = &cobra.Command{
	Use:   "deprecate",
	Short: "Deprecate one skill revision (or all revisions for a skill).",
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.TrimSpace(userSkillShowID)
		if query == "" {
			fmt.Println("Error: --id is required")
			return
		}
		rec, err := resolveRequestedSkillSelection(query)
		if err != nil || rec == nil {
			fmt.Printf("Skill not found: %s\n", query)
			return
		}
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		if err := reg.DeprecateRevision(rec.SkillID, strings.TrimSpace(userSkillRevisionID)); err != nil {
			fmt.Printf("Error deprecating revision: %v\n", err)
			return
		}
		fmt.Printf("Deprecated skill revision(s).\nskill_id=%s\nrevision_id=%s\n", rec.SkillID, valueOrPlaceholder(strings.TrimSpace(userSkillRevisionID)))
	},
}

var skillsRevisionsCmd = &cobra.Command{
	Use:   "revisions",
	Short: "List revisions for one skill.",
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.TrimSpace(userSkillShowID)
		if query == "" {
			fmt.Println("Error: --id is required")
			return
		}
		rec, err := resolveRequestedSkillSelection(query)
		if err != nil || rec == nil {
			fmt.Printf("Skill not found: %s\n", query)
			return
		}
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		revs, err := reg.ListRevisions(rec.SkillID)
		if err != nil {
			fmt.Printf("Error listing revisions: %v\n", err)
			return
		}
		if len(revs) == 0 {
			fmt.Println("No revisions found.")
			return
		}
		fmt.Printf("TALOS SKILL REVISIONS\n\nCOMMAND\n  talos skills revisions --id %s\n\nSTATUS\n  SUCCESS\n\nSUMMARY\n  skill_id: %s\n  count: %d\n\nREVISIONS\n", rec.SkillID, rec.SkillID, len(revs))
		for i, r := range revs {
			fmt.Printf("  %d. revision=%s version=%s status=%s active=%t updated=%s\n", i+1, valueOrPlaceholder(r.RevisionID), valueOrPlaceholder(r.Version), valueOrPlaceholder(r.Status), r.Active, formatSkillTime(r.UpdatedAt))
		}
	},
}

var skillsExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export a signed skill bundle for a specific revision.",
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.TrimSpace(userSkillShowID)
		if query == "" {
			fmt.Println("Error: --id is required")
			return
		}
		out := strings.TrimSpace(userSkillExportOut)
		if out == "" {
			fmt.Println("Error: --out is required")
			return
		}
		rec, err := resolveRequestedSkillSelection(query)
		if err != nil || rec == nil {
			fmt.Printf("Skill not found: %s\n", query)
			return
		}
		target := *rec
		if strings.TrimSpace(userSkillRevisionID) != "" {
			reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
			revs, err := reg.ListRevisions(rec.SkillID)
			if err == nil {
				for _, r := range revs {
					if strings.EqualFold(strings.TrimSpace(r.RevisionID), strings.TrimSpace(userSkillRevisionID)) {
						target = r
						break
					}
				}
			}
		}
		if err := skills.ExportBundle(target, out); err != nil {
			fmt.Printf("Error exporting bundle: %v\n", err)
			return
		}
		fmt.Printf("Exported bundle.\nout=%s\nskill_id=%s\nrevision_id=%s\n", out, target.SkillID, target.RevisionID)
	},
}

var skillsImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a signed skill bundle.",
	Run: func(cmd *cobra.Command, args []string) {
		in := strings.TrimSpace(userSkillImportIn)
		if in == "" {
			fmt.Println("Error: --in is required")
			return
		}
		rec, err := skills.ImportBundle(skills.PermanentSkillsRoot(), in)
		if err != nil {
			fmt.Printf("Error importing bundle: %v\n", err)
			return
		}
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		if err := reg.Upsert(rec); err != nil {
			fmt.Printf("Error writing imported skill: %v\n", err)
			return
		}
		fmt.Printf("Imported bundle.\nskill_id=%s\nrevision_id=%s\nstatus=%s\n", rec.SkillID, rec.RevisionID, rec.Status)
	},
}

var skillsMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate legacy skill registry entries to V3 metadata.",
	Run: func(cmd *cobra.Command, args []string) {
		w := cmd.OutOrStdout()
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		report, err := reg.MigrateLegacy(userSkillMigrateApply)
		if err != nil {
			fmt.Fprintf(w, "Error migrating skills registry: %v\n", err)
			return
		}
		mode := "DRY-RUN"
		if userSkillMigrateApply {
			mode = "APPLIED"
		}
		fmt.Fprintf(w, "TALOS SKILLS MIGRATE\n\nCOMMAND\n  talos skills migrate\n\nSTATUS\n  SUCCESS\n\nMODE\n  %s\n\nSUMMARY\n", mode)
		fmt.Fprintf(w, "  total_records: %d\n", report.TotalRecords)
		fmt.Fprintf(w, "  legacy_records: %d\n", report.LegacyRecords)
		fmt.Fprintf(w, "  updated_records: %d\n", report.UpdatedRecords)
		if userSkillMigrateApply {
			fmt.Fprintln(w, "\nRESULT\n  Registry migration completed.\n\nNEXT\n  Use: talos skills list")
		} else {
			fmt.Fprintln(w, "\nRESULT\n  No files written. Re-run with --apply to persist migration.\n\nNEXT\n  Use: talos skills migrate --apply")
		}
	},
}

func init() {
	skillsCreateCmd.Flags().StringVar(&userSkillName, "name", "", "Skill name")
	skillsCreateCmd.Flags().StringVar(&userSkillIntent, "intent", "", "Primary intent/capability description")
	skillsCreateCmd.Flags().StringVar(&userSkillDescription, "description", "", "Description (also used as intent if --intent is omitted)")
	skillsCreateCmd.Flags().StringVar(&userSkillReasoningTier, "reasoning-tier", "t2", "Reasoning tier tag")
	skillsCreateCmd.Flags().StringVar(&userSkillTaskType, "task-type", "general", "Task type tag")
	skillsCreateCmd.Flags().StringSliceVar(&userSkillRequestedTools, "requested-tool", nil, "Requested tool in kind:name format (repeatable)")
	skillsCreateCmd.Flags().StringSliceVar(&userSkillRequestedDomains, "requested-domain", nil, "Requested external domain (repeatable)")

	skillsPreflightCmd.Flags().StringVar(&userSkillName, "name", "", "Skill name")
	skillsPreflightCmd.Flags().StringVar(&userSkillIntent, "intent", "", "Primary intent/capability description")
	skillsPreflightCmd.Flags().StringVar(&userSkillDescription, "description", "", "Description (also used as intent if --intent is omitted)")
	skillsPreflightCmd.Flags().StringSliceVar(&userSkillRequestedTools, "requested-tool", nil, "Requested tool in kind:name format (repeatable)")
	skillsPreflightCmd.Flags().StringSliceVar(&userSkillRequestedDomains, "requested-domain", nil, "Requested external domain (repeatable)")

	skillsShowCmd.Flags().StringVar(&userSkillShowID, "id", "", "Skill ID, name, or path fragment")
	skillsValidateCmd.Flags().StringVar(&userSkillShowID, "id", "", "Skill ID/name/path fragment")
	skillsValidateCmd.Flags().StringVar(&userSkillRevisionID, "revision", "", "Revision ID (optional; defaults latest)")
	skillsActivateCmd.Flags().StringVar(&userSkillShowID, "id", "", "Skill ID/name/path fragment")
	skillsActivateCmd.Flags().StringVar(&userSkillRevisionID, "revision", "", "Revision ID (optional; defaults latest)")
	skillsDeprecateCmd.Flags().StringVar(&userSkillShowID, "id", "", "Skill ID/name/path fragment")
	skillsDeprecateCmd.Flags().StringVar(&userSkillRevisionID, "revision", "", "Revision ID (optional; empty means all)")
	skillsRevisionsCmd.Flags().StringVar(&userSkillShowID, "id", "", "Skill ID/name/path fragment")
	skillsExportCmd.Flags().StringVar(&userSkillShowID, "id", "", "Skill ID/name/path fragment")
	skillsExportCmd.Flags().StringVar(&userSkillRevisionID, "revision", "", "Revision ID (optional; defaults active/latest)")
	skillsExportCmd.Flags().StringVar(&userSkillExportOut, "out", "", "Output bundle path")
	skillsImportCmd.Flags().StringVar(&userSkillImportIn, "in", "", "Input bundle path")
	skillsMigrateCmd.Flags().BoolVar(&userSkillMigrateApply, "apply", false, "Persist migrated V3 metadata to registry index")

	skillsCmd.AddCommand(skillsCreateCmd)
	skillsCmd.AddCommand(skillsPreflightCmd)
	skillsCmd.AddCommand(skillsListCmd)
	skillsCmd.AddCommand(skillsShowCmd)
	skillsCmd.AddCommand(skillsValidateCmd)
	skillsCmd.AddCommand(skillsActivateCmd)
	skillsCmd.AddCommand(skillsDeprecateCmd)
	skillsCmd.AddCommand(skillsRevisionsCmd)
	skillsCmd.AddCommand(skillsExportCmd)
	skillsCmd.AddCommand(skillsImportCmd)
	skillsCmd.AddCommand(skillsMigrateCmd)
	rootCmd.AddCommand(skillsCmd)
}

func parseRequestedTools(raw []string) ([]tools.SkillPreflightToolRequest, error) {
	out := make([]tools.SkillPreflightToolRequest, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("invalid format %q, expected kind:name", item)
		}
		out = append(out, tools.SkillPreflightToolRequest{
			Kind: strings.TrimSpace(parts[0]),
			Name: strings.TrimSpace(parts[1]),
		})
	}
	return out, nil
}

func listSkillManifests(root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), "skill_manifest.json") {
			out = append(out, path)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

func readSkillIdentity(path string) (id, name string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return "", ""
	}
	id = strings.TrimSpace(fmt.Sprintf("%v", raw["skill_id"]))
	name = strings.TrimSpace(fmt.Sprintf("%v", raw["name"]))
	if id == "<nil>" {
		id = ""
	}
	if name == "<nil>" {
		name = ""
	}
	return id, name
}

func dedupeSkillStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		t := strings.TrimSpace(v)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

func resolvedSkillIntent(intent, description string) string {
	intent = strings.TrimSpace(intent)
	if intent != "" {
		return intent
	}
	return strings.TrimSpace(description)
}

func valueOrPlaceholder(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "n/a"
	}
	return s
}

func formatSkillTime(ts time.Time) string {
	if ts.IsZero() {
		return "n/a"
	}
	return ts.UTC().Format(time.RFC3339)
}
