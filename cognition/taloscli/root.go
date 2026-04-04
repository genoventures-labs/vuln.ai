package taloscli

import (
	"fmt"
	"os"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/spf13/cobra"
)

var requestedSkill string
var requestedNamespace string

const talosHelpTemplate = `TALOS CLI

USAGE
  {{.UseLine}}

{{- if .Long}}
DESCRIPTION
  {{.Long}}

{{- end}}
{{- if .HasAvailableSubCommands}}
AVAILABLE COMMANDS
{{- range .Commands}}
{{- if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }}  {{.Short}}
{{- end}}
{{- end}}

{{- end}}
{{- if .HasAvailableLocalFlags}}
FLAGS
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}

{{- end}}
{{- if .HasAvailableInheritedFlags}}
GLOBAL FLAGS
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}

{{- end}}
{{- if .Example}}
EXAMPLES
{{.Example}}

{{- end}}
MORE INFO
  Use "{{.CommandPath}} [command] --help" for more details on a command.
`

var nextCmd = &cobra.Command{
	Use:     "next",
	Aliases: []string{"/next"},
	Short:   "Deprecated alias for root help (pagination removed).",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "Help pagination retired; showing full root help.")
		fmt.Fprintln(cmd.OutOrStdout())
		renderRootHelpPage(cmd.OutOrStdout(), true)
	},
}

var rootCmd = &cobra.Command{
	Use:     "talos",
	Aliases: []string{"personal-llm"},
	Short:   "TALOS CLI interface powered by your Ollama VPS.",
	Long: `talos is a CLI tool to interact with your personal LLM hosted on an Ollama VPS.
It leverages the JIT model router to ensure optimal models are available.

Use chat domain pinning (--domain / --namespace / PLM_CHAT_DOMAIN) plus learn namespaces (--namespace) to isolate work domains under zero-trust retrieval.
Explicit namespace selections are persisted and reused across later runs when not overridden.`,
	Example: `  talos chat "Summarize latest telemetry"
    talos --namespace talos-runtime research run "What changed in X this week?"
    talos chat --domain talos-runtime "Summarize latest telemetry"
    talos chat --namespace talos-runtime "Summarize latest telemetry"
    talos chat "Summarize recent learn sessions" --text-gen
   PLM_CHAT_TEXT_GEN_PRIMARY=1 talos chat "Summarize recent learn sessions"
   talos chat --cognition minimal --timeout-profile quick "Online?"
  talos --skill report2markdown chat "Convert this into markdown release notes"
  talos research run "What changed in X this week?"
  talos research run --profile market-scan "What changed in X this week?"
  talos research run --category crypto "What changed in X this week?"
  talos research run --profile market-scan --dry-run
  talos research profiles create --name market-scan --category crypto --query-template "Weekly market scan" --max-pages 60 --crawl-depth 2
  talos research profiles set-default --name market-scan
  talos learn --profile hf-train-default --dry-run
  talos learn profile export --out .memory/talos_profiles_bundle.json
  talos learn profile import --in .memory/talos_profiles_bundle.json --merge
  talos learn profile-gen security --set-default
  talos research profile-gen compliance --set-default
  talos learn profile-gen --list-domains
  talos benchmark full
   talos multi-agent "Design a rollout plan" --mode planning
   talos tasks show
   talos tasks next
   talos tasks update --id task-1 --status done
   talos multi-agent "Analyze release risk" --agents planner,researcher,verifier,synthesizer
  talos version
  talos update check
  talos completion bash > ~/.local/share/bash-completion/completions/talos
  talos --help
  talos find research
  talos doc-search "Sasswall" --dir ./docs
  talos pipeline "research run 'What changed in X this week?' --skill analyst ; learn --from-research latest --skill memory_curator"
  talos pipeline "research run 'What changed in X this week?' ; learn --from-research latest"
  talos "research run 'What changed in X this week?' ; learn --from-research latest"
  talos learn profile create --name hf-train-default --hf-dataset TeichAI/claude-4.5-opus-high-reasoning-250x --hf-config default --hf-split train
   talos learn --profile hf-train-default
   talos learn --dir ./docs --namespace talos-runtime
   talos learn --self-train-speech "Capture my concise ops tone"
   talos learn --synthetic-text-out .memory/synthetic/train.jsonl "Capture stable native text-gen examples"
   talos learn "Store this project note"
  talos learned --last 5
   talos skills create --name report2markdown --description "Converts research reports to markdown files"
   talos skills preflight --name report2markdown --description "Converts research reports to markdown files"
   talos namespace bind-tool --id web_search
   talos namespace bind-skill --id skill_report2markdown
   talos namespace show
   talos namespace clear
   talos tools admin list
  talos skills list
  talos release-check`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && strings.TrimSpace(requestedNamespace) != "" {
			return nil
		}
		if len(args) == 1 {
			chain := strings.TrimSpace(args[0])
			if strings.Contains(chain, ";") || strings.Contains(chain, "|") {
				if strings.TrimSpace(requestedSkill) != "" {
					return fmt.Errorf("global --skill is not supported for pipeline chains; use per-step --skill within the quoted chain")
				}
				return runPipelineChain(chain)
			}
		}
		return cmd.Help()
	},
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		path := strings.ToLower(strings.TrimSpace(cmd.CommandPath()))
		if strings.Contains(path, " update") || strings.HasSuffix(path, " version") || strings.HasSuffix(path, " release-check") || strings.HasSuffix(path, " next") || strings.HasSuffix(path, " /next") {
			return
		}
		if ns := strings.ToLower(strings.TrimSpace(requestedNamespace)); ns != "" {
			if sm, err := state.NewManager(); err == nil {
				sm.SetActiveNamespace(ns)
				if err := sm.Save(); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Warning: failed to persist namespace: %v\n", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "NAMESPACE ACTIVE: %s\n", ns)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: failed to initialize state manager: %v\n", err)
			}
		}
		maybeAutoUpdate()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.SetHelpTemplate(talosHelpTemplate)
	rootCmd.SetUsageTemplate(talosHelpTemplate)
	defaultHelpFunc := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == rootCmd {
			renderRootHelpPage(cmd.OutOrStdout(), true)
			return
		}
		defaultHelpFunc(cmd, args)
	})
	rootCmd.PersistentFlags().StringVar(&requestedSkill, "skill", "", "Use a specific enabled TALOS skill by ID, name, or path fragment")
	rootCmd.PersistentFlags().StringVar(&requestedNamespace, "namespace", "", "Set and persist active workspace namespace for all commands in this run")
	rootCmd.AddCommand(nextCmd)
}
