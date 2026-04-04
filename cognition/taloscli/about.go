package taloscli

import (
	"io"

	"github.com/spf13/cobra"
)

var aboutCmd = &cobra.Command{
	Use:   "about",
	Short: "Show TALOS identity, mission, and architecture summary.",
	Long:  "Displays a professional ASCII overview of TALOS based on the repository architecture documents.",
	Run: func(cmd *cobra.Command, args []string) {
		writeAbout(cmd.OutOrStdout())
	},
}

func writeAbout(w io.Writer) {
	_, _ = io.WriteString(w, `TALOS ABOUT

IDENTITY
  Name: T.A.L.O.S. (Thynaptic Autonomous Local Operational Sovereign)
  Role: Sovereign Sentinel runtime for local-first engineering orchestration.
  Classification: Level 5 Autonomous Sentinel.

MISSION
  1. Keep data sovereignty intact through local-first operation.
  2. Provide evidence-based engineering support and truth arbitration.
  3. Evolve capabilities through controlled, policy-gated synthesis.

ARCHITECTURE
  Mind Layer: Aether (federated cognitive synthesis and reasoning).
  Action Layer: S.T.R.A.T.A. (sovereign tool runtime and tooling architecture).
  Sentinel Layer: TALOS (operator-facing executive pilot).

CORE OPERATING MODEL
  1. Observe: gather workspace and runtime signals.
  2. Orient: reason with memory, context, and policy.
  3. Decide: choose bounded actions with verification.
  4. Act: execute through local skills and tool substrate.

SOVEREIGN BOUNDARY
  Skills are internal cognitive capabilities persisted locally.
  Tools are environmental capabilities routed through S.T.R.A.T.A.
  Preflight policy checks gate risky synthesis and external actions.

DAEMON ECOSYSTEM
  reflex-daemon: runtime pressure and anomaly monitoring.
  scout-daemon: contradiction detection and evidence scanning.
  archive-daemon: decision and reasoning trace archival.
  planner-daemon: roadmap decomposition and mission planning.
  compliance-daemon: advisory security and policy hardening.
  lab-assistant: shadow fix and verification workflow.

AUTHORITATIVE DOCUMENTS
  docs/T.A.L.O.S. Sentinel Architecture Whitepaper.md
  docs/Aether v1.1.md
  docs/STRATA_ Sovereign AI Tool Foundry.md
  README_TALOS.md

SIGNATURE
  TALOS // AETHER-V1.1 // STRATA-RESONANT // THYNAPTIC-SOVEREIGN
`)
}

func init() {
	rootCmd.AddCommand(aboutCmd)
}
