package taloscli

import (
	"fmt"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/spf13/cobra"
)

var namespaceCmd = &cobra.Command{
	Use:   "namespace",
	Short: "Manage active workspace namespace scope.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if namespaceShowFlag {
			runNamespaceShow(cmd)
			return nil
		}
		if namespaceClearFlag {
			runNamespaceClear(cmd)
			return nil
		}
		if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
			runNamespaceActivate(cmd, args[0])
			return nil
		}
		return cmd.Help()
	},
}

var (
	namespaceShowFlag       bool
	namespaceClearFlag      bool
	namespaceManagerFactory = state.NewManager
	namespaceCapabilityNS   string
	namespaceSkillID        string
	namespaceToolID         string
)

var namespaceClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear persisted active workspace namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		runNamespaceClear(cmd)
	},
}

var namespaceShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current persisted active workspace namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		runNamespaceShow(cmd)
	},
}

var namespaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active and inactive namespaces.",
	Run: func(cmd *cobra.Command, args []string) {
		sm, err := namespaceManagerFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error initializing state manager: %v\n", err)
			return
		}
		active := strings.ToLower(strings.TrimSpace(sm.ActiveNamespace()))
		store, err := namespaceCapabilityStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading namespace capability policy: %v\n", err)
			return
		}
		namespaces := store.Namespaces()
		seen := map[string]bool{}
		out := make([]string, 0, len(namespaces)+1)
		for _, ns := range namespaces {
			ns = strings.ToLower(strings.TrimSpace(ns))
			if ns == "" || seen[ns] {
				continue
			}
			seen[ns] = true
			out = append(out, ns)
		}
		if active != "" && !seen[active] {
			out = append([]string{active}, out...)
		}
		if len(out) == 0 {
			printCommandStatus(cmd.OutOrStdout(), "talos namespace list", "success")
			printSection(cmd.OutOrStdout(), "summary")
			printKV(cmd.OutOrStdout(), "namespaces", "0")
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos namespace list", "success")
		printSection(cmd.OutOrStdout(), "namespaces")
		for _, ns := range out {
			status := "inactive"
			if ns == active {
				status = "active"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s [%s]\n", ns, status)
		}
	},
}

var namespaceBindingsCmd = &cobra.Command{
	Use:   "bindings",
	Short: "Show skill/tool bindings for a namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, namespaceCapabilityNS)
		if !ok {
			return
		}
		store, err := namespaceCapabilityStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading namespace capability policy: %v\n", err)
			return
		}
		caps := store.Capabilities(ns)
		printCommandStatus(cmd.OutOrStdout(), "talos namespace bindings", "success")
		printSection(cmd.OutOrStdout(), "target")
		printKV(cmd.OutOrStdout(), "namespace", ns)
		printSection(cmd.OutOrStdout(), "skills")
		if len(caps.Skills) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "  - none")
		} else {
			for _, id := range caps.Skills {
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", id)
			}
		}
		printSection(cmd.OutOrStdout(), "tools")
		if len(caps.Tools) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "  - none")
			return
		}
		for _, id := range caps.Tools {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", id)
		}
	},
}

var namespaceBindSkillCmd = &cobra.Command{
	Use:   "bind-skill",
	Short: "Allow a skill in a namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, namespaceCapabilityNS)
		if !ok {
			return
		}
		id := strings.ToLower(strings.TrimSpace(namespaceSkillID))
		if id == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "Error: --id is required")
			return
		}
		store, err := namespaceCapabilityStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading namespace capability policy: %v\n", err)
			return
		}
		store.AllowSkill(ns, id)
		if err := store.Save(); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error saving namespace capability policy: %v\n", err)
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos namespace bind-skill", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "bound_skill", id)
		printKV(cmd.OutOrStdout(), "namespace", ns)
	},
}

var namespaceUnbindSkillCmd = &cobra.Command{
	Use:   "unbind-skill",
	Short: "Remove a skill binding from a namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, namespaceCapabilityNS)
		if !ok {
			return
		}
		id := strings.ToLower(strings.TrimSpace(namespaceSkillID))
		if id == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "Error: --id is required")
			return
		}
		store, err := namespaceCapabilityStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading namespace capability policy: %v\n", err)
			return
		}
		store.DenySkill(ns, id)
		if err := store.Save(); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error saving namespace capability policy: %v\n", err)
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos namespace unbind-skill", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "unbound_skill", id)
		printKV(cmd.OutOrStdout(), "namespace", ns)
	},
}

var namespaceBindToolCmd = &cobra.Command{
	Use:   "bind-tool",
	Short: "Allow a tool/plugin in a namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, namespaceCapabilityNS)
		if !ok {
			return
		}
		id := strings.ToLower(strings.TrimSpace(namespaceToolID))
		if id == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "Error: --id is required")
			return
		}
		store, err := namespaceCapabilityStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading namespace capability policy: %v\n", err)
			return
		}
		store.AllowTool(ns, id)
		if err := store.Save(); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error saving namespace capability policy: %v\n", err)
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos namespace bind-tool", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "bound_tool", id)
		printKV(cmd.OutOrStdout(), "namespace", ns)
	},
}

var namespaceUnbindToolCmd = &cobra.Command{
	Use:   "unbind-tool",
	Short: "Remove a tool/plugin binding from a namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, namespaceCapabilityNS)
		if !ok {
			return
		}
		id := strings.ToLower(strings.TrimSpace(namespaceToolID))
		if id == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "Error: --id is required")
			return
		}
		store, err := namespaceCapabilityStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading namespace capability policy: %v\n", err)
			return
		}
		store.DenyTool(ns, id)
		if err := store.Save(); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error saving namespace capability policy: %v\n", err)
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos namespace unbind-tool", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "unbound_tool", id)
		printKV(cmd.OutOrStdout(), "namespace", ns)
	},
}

func runNamespaceClear(cmd *cobra.Command) {
	sm, err := namespaceManagerFactory()
	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Error initializing state manager: %v\n", err)
		return
	}
	sm.SetActiveNamespace("")
	if err := sm.Save(); err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Error clearing namespace: %v\n", err)
		return
	}
	printCommandStatus(cmd.OutOrStdout(), "talos namespace clear", "success")
	printSection(cmd.OutOrStdout(), "result")
	printKV(cmd.OutOrStdout(), "namespace", "none")
}

func runNamespaceShow(cmd *cobra.Command) {
	sm, err := namespaceManagerFactory()
	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Error initializing state manager: %v\n", err)
		return
	}
	ns := strings.TrimSpace(sm.ActiveNamespace())
	if ns == "" {
		ns = "none"
	}
	printCommandStatus(cmd.OutOrStdout(), "talos namespace show", "success")
	printSection(cmd.OutOrStdout(), "result")
	printKV(cmd.OutOrStdout(), "namespace", ns)
}

func runNamespaceActivate(cmd *cobra.Command, namespace string) {
	ns := strings.ToLower(strings.TrimSpace(namespace))
	if ns == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Error: namespace is required")
		return
	}
	sm, err := namespaceManagerFactory()
	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Error initializing state manager: %v\n", err)
		return
	}
	sm.SetActiveNamespace(ns)
	if err := sm.Save(); err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Error setting namespace: %v\n", err)
		return
	}
	printCommandStatus(cmd.OutOrStdout(), "talos namespace", "success")
	printSection(cmd.OutOrStdout(), "result")
	printKV(cmd.OutOrStdout(), "namespace_active", ns)
}

func resolveNamespaceTarget(cmd *cobra.Command, explicit string) (string, bool) {
	ns := strings.ToLower(strings.TrimSpace(explicit))
	if ns == "" {
		sm, err := namespaceManagerFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error initializing state manager: %v\n", err)
			return "", false
		}
		ns = strings.ToLower(strings.TrimSpace(sm.ActiveNamespace()))
	}
	if ns == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Error: namespace is required (set active namespace or pass --namespace)")
		return "", false
	}
	return ns, true
}

func init() {
	namespaceCmd.Flags().BoolVar(&namespaceShowFlag, "show", false, "show current persisted active workspace namespace")
	namespaceCmd.Flags().BoolVar(&namespaceClearFlag, "clear", false, "clear persisted active workspace namespace")
	namespaceBindingsCmd.Flags().StringVar(&namespaceCapabilityNS, "namespace", "", "target namespace (defaults to active namespace)")
	namespaceBindSkillCmd.Flags().StringVar(&namespaceCapabilityNS, "namespace", "", "target namespace (defaults to active namespace)")
	namespaceBindSkillCmd.Flags().StringVar(&namespaceSkillID, "id", "", "skill id to bind")
	namespaceUnbindSkillCmd.Flags().StringVar(&namespaceCapabilityNS, "namespace", "", "target namespace (defaults to active namespace)")
	namespaceUnbindSkillCmd.Flags().StringVar(&namespaceSkillID, "id", "", "skill id to unbind")
	namespaceBindToolCmd.Flags().StringVar(&namespaceCapabilityNS, "namespace", "", "target namespace (defaults to active namespace)")
	namespaceBindToolCmd.Flags().StringVar(&namespaceToolID, "id", "", "tool/plugin id to bind")
	namespaceUnbindToolCmd.Flags().StringVar(&namespaceCapabilityNS, "namespace", "", "target namespace (defaults to active namespace)")
	namespaceUnbindToolCmd.Flags().StringVar(&namespaceToolID, "id", "", "tool/plugin id to unbind")
	namespaceCmd.AddCommand(namespaceShowCmd)
	namespaceCmd.AddCommand(namespaceListCmd)
	namespaceCmd.AddCommand(namespaceClearCmd)
	namespaceCmd.AddCommand(namespaceBindingsCmd)
	namespaceCmd.AddCommand(namespaceBindSkillCmd)
	namespaceCmd.AddCommand(namespaceUnbindSkillCmd)
	namespaceCmd.AddCommand(namespaceBindToolCmd)
	namespaceCmd.AddCommand(namespaceUnbindToolCmd)
	rootCmd.AddCommand(namespaceCmd)
}
