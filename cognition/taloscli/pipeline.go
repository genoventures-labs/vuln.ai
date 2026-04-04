package taloscli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/spf13/cobra"
)

type pipelineStep struct {
	Raw      string
	Args     []string
	SkillRef string
}

type pipelineState struct {
	LastResearchSessionID string
}

var pipelineSkillResolver = resolveRequestedSkillSelection

var pipelineCmd = &cobra.Command{
	Use:   "pipeline <chain>",
	Short: "Execute an allowlisted TALOS command chain.",
	Long:  "Executes a strict, allowlisted pipeline such as research -> learn with structured handoff.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		chain := strings.TrimSpace(strings.Join(args, " "))
		if chain == "" {
			return fmt.Errorf("pipeline chain cannot be empty")
		}
		return runPipelineChain(chain)
	},
}

func init() {
	rootCmd.AddCommand(pipelineCmd)
}

func runPipelineChain(chain string) error {
	steps, err := parsePipelineChain(chain)
	if err != nil {
		return err
	}
	skillBindings, err := validatePipelineSteps(steps)
	if err != nil {
		return err
	}
	state := &pipelineState{}
	for i, step := range steps {
		if err := executePipelineStep(step, state, skillBindings[i]); err != nil {
			return fmt.Errorf("pipeline failed at step %d (%s): %w", i+1, step.Raw, err)
		}
	}
	printCommandStatus(os.Stdout, "talos pipeline", "success")
	printSection(os.Stdout, "steps")
	printKV(os.Stdout, "executed", fmt.Sprintf("%d", len(steps)))
	return nil
}

func parsePipelineChain(chain string) ([]pipelineStep, error) {
	segments, err := splitPipelineSegments(chain)
	if err != nil {
		return nil, err
	}
	steps := make([]pipelineStep, 0, len(segments))
	for _, seg := range segments {
		argv, err := splitShellTokens(seg)
		if err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", seg, err)
		}
		if len(argv) == 0 {
			continue
		}
		skillRef, cleanedArgs, err := extractPipelineStepSkill(argv)
		if err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", seg, err)
		}
		if len(cleanedArgs) == 0 {
			return nil, fmt.Errorf("invalid step %q: command tokens missing after skill extraction", seg)
		}
		steps = append(steps, pipelineStep{
			Raw:      strings.TrimSpace(seg),
			Args:     cleanedArgs,
			SkillRef: skillRef,
		})
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("no pipeline steps found")
	}
	return steps, nil
}

func extractPipelineStepSkill(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, nil
	}
	cleaned := make([]string, 0, len(args))
	skillRef := ""
	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])
		if strings.EqualFold(tok, "--skill") {
			if skillRef != "" {
				return "", nil, fmt.Errorf("duplicate --skill flag")
			}
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--skill requires a value")
			}
			next := strings.TrimSpace(args[i+1])
			if next == "" || strings.HasPrefix(next, "--") {
				return "", nil, fmt.Errorf("--skill requires a non-flag value")
			}
			skillRef = next
			i++
			continue
		}
		lowerTok := strings.ToLower(tok)
		if strings.HasPrefix(lowerTok, "--skill=") {
			if skillRef != "" {
				return "", nil, fmt.Errorf("duplicate --skill flag")
			}
			val := strings.TrimSpace(tok[len("--skill="):])
			if val == "" {
				return "", nil, fmt.Errorf("--skill requires a value")
			}
			skillRef = val
			continue
		}
		cleaned = append(cleaned, args[i])
	}
	return strings.TrimSpace(skillRef), cleaned, nil
}

func splitPipelineSegments(chain string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for _, r := range chain {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
			cur.WriteRune(r)
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			cur.WriteRune(r)
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			cur.WriteRune(r)
		case ';', '|':
			if inSingle || inDouble {
				cur.WriteRune(r)
				continue
			}
			piece := strings.TrimSpace(cur.String())
			if piece != "" {
				out = append(out, piece)
			}
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if escaped || inSingle || inDouble {
		return nil, errors.New("unterminated quote or escape in chain")
	}
	if piece := strings.TrimSpace(cur.String()); piece != "" {
		out = append(out, piece)
	}
	return out, nil
}

func splitShellTokens(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		args = append(args, cur.String())
		cur.Reset()
	}
	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '\'':
			if inDouble {
				cur.WriteRune(r)
			} else {
				inSingle = !inSingle
			}
		case '"':
			if inSingle {
				cur.WriteRune(r)
			} else {
				inDouble = !inDouble
			}
		case ' ', '\t', '\n':
			if inSingle || inDouble {
				cur.WriteRune(r)
			} else {
				flush()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if escaped || inSingle || inDouble {
		return nil, errors.New("unterminated quote or escape")
	}
	flush()
	return args, nil
}

func normalizePipelineAction(step pipelineStep) string {
	if len(step.Args) == 0 {
		return ""
	}
	if strings.EqualFold(step.Args[0], "research") {
		if len(step.Args) > 1 {
			if strings.EqualFold(step.Args[1], "run") {
				return "research/run"
			}
			if strings.EqualFold(step.Args[1], "deep") {
				return "research/deep"
			}
		}
		return "research/run"
	}
	if strings.EqualFold(step.Args[0], "learn") {
		for i := 1; i < len(step.Args); i++ {
			tok := step.Args[i]
			if strings.EqualFold(tok, "--from-research") || strings.HasPrefix(strings.ToLower(tok), "--from-research=") {
				return "learn/from-research"
			}
		}
		return "learn/other"
	}
	return strings.ToLower(step.Args[0])
}

func validatePipelineSteps(steps []pipelineStep) ([]*skills.SkillRecord, error) {
	if len(steps) < 2 {
		return nil, fmt.Errorf("pipeline must contain at least 2 steps")
	}
	skillBindings := make([]*skills.SkillRecord, len(steps))
	for i, step := range steps {
		if strings.TrimSpace(step.SkillRef) == "" {
			continue
		}
		rec, err := pipelineSkillResolver(step.SkillRef)
		if err != nil {
			return nil, fmt.Errorf("step %d skill resolution failed (%s): %w", i+1, step.SkillRef, err)
		}
		if rec == nil {
			return nil, fmt.Errorf("step %d skill resolution failed (%s): no matching enabled skill", i+1, step.SkillRef)
		}
		skillBindings[i] = rec
	}
	for i := 0; i < len(steps)-1; i++ {
		from := normalizePipelineAction(steps[i])
		to := normalizePipelineAction(steps[i+1])
		if (from == "research/run" || from == "research/deep") && to == "learn/from-research" {
			continue
		}
		return nil, fmt.Errorf("invalid chain transition: %s -> %s (allowed: research run|deep -> learn --from-research)", from, to)
	}
	return skillBindings, nil
}

func executePipelineStep(step pipelineStep, st *pipelineState, skillRec *skills.SkillRecord) error {
	if skillRec != nil {
		printSection(os.Stdout, "pipeline_step")
		printKV(os.Stdout, "skill_id", strings.TrimSpace(skillRec.SkillID))
		printKV(os.Stdout, "skill_name", strings.TrimSpace(skillRec.Name))
	}
	action := normalizePipelineAction(step)
	switch action {
	case "research/run":
		mode := "run"
		idx := 1
		if len(step.Args) > 1 && (strings.EqualFold(step.Args[1], "run") || strings.EqualFold(step.Args[1], "deep")) {
			mode = strings.ToLower(step.Args[1])
			idx = 2
		}
		query := strings.TrimSpace(strings.Join(step.Args[idx:], " "))
		if query == "" {
			return fmt.Errorf("research query cannot be empty")
		}
		report, sessionID, err := executeResearchMode(mode, query, researchExecutionContext{})
		if err != nil {
			return err
		}
		_ = report
		st.LastResearchSessionID = sessionID
		return nil
	case "research/deep":
		query := ""
		if len(step.Args) > 2 {
			query = strings.TrimSpace(strings.Join(step.Args[2:], " "))
		}
		if query == "" {
			return fmt.Errorf("research query cannot be empty")
		}
		report, sessionID, err := executeResearchMode("deep", query, researchExecutionContext{})
		if err != nil {
			return err
		}
		_ = report
		st.LastResearchSessionID = sessionID
		return nil
	case "learn/from-research":
		artifactID := "latest"
		for i := 1; i < len(step.Args); i++ {
			tok := step.Args[i]
			if strings.HasPrefix(strings.ToLower(tok), "--from-research=") {
				artifactID = strings.TrimSpace(strings.SplitN(tok, "=", 2)[1])
			}
			if strings.EqualFold(tok, "--from-research") {
				if i+1 < len(step.Args) && !strings.HasPrefix(step.Args[i+1], "--") {
					artifactID = strings.TrimSpace(step.Args[i+1])
				}
			}
		}
		if strings.EqualFold(artifactID, "latest") && strings.TrimSpace(st.LastResearchSessionID) != "" {
			artifactID = st.LastResearchSessionID
		}
		return executeLearnFromResearch(artifactID, true, true)
	default:
		return fmt.Errorf("unsupported pipeline step: %s", step.Raw)
	}
}
