package taloscli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
	"github.com/spf13/cobra"
)

var adminCreateClientID string
var adminRotateClientID string
var adminDeleteClientID string
var adminDeleteYes bool
var adminPluginID string
var adminPluginSource string
var adminPluginManifestPath string
var adminToolgenName string
var adminToolgenDescription string
var adminToolgenPublisher string
var adminToolgenGoal string
var adminToolgenRequirement string
var adminToolgenMetadata string
var adminToolgenJobID string

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Toolserver and external tool utilities.",
}

var toolsAdminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Manage GLM Toolserver client credentials via admin endpoints.",
	Long: `Admin endpoints require GLM_ADMIN_TOKEN and are typically localhost-only.

Uses GLM_TOOLSERVER_BASE_URL when set, otherwise defaults to the configured toolserver URL.`,
}

var toolsAdminListCmd = &cobra.Command{
	Use:   "list",
	Short: "List provisioned toolserver clients.",
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin list", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		clients, err := ac.ListClients()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin list", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("listing clients: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin list", "success")
		printSection(cmd.OutOrStdout(), "results")
		printKV(cmd.OutOrStdout(), "client_count", fmt.Sprintf("%d", len(clients)))
		if len(clients) == 0 {
			return
		}
		printSection(cmd.OutOrStdout(), "clients")
		for _, c := range clients {
			line := "- " + strings.TrimSpace(c.ClientID)
			if !c.CreatedAt.IsZero() {
				line += " (created " + c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z") + ")"
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
	},
}

var toolsAdminCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new toolserver client keypair.",
	Long:  "Returns client_id and api_key once. Store api_key securely.",
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin create", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		resp, err := ac.CreateClient(strings.TrimSpace(adminCreateClientID))
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin create", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("creating client: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin create", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "glm_client_id", strings.TrimSpace(resp.ClientID))
		printKV(cmd.OutOrStdout(), "glm_api_key", strings.TrimSpace(resp.APIKey))
	},
}

var toolsAdminRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Rotate an existing client API key.",
	Run: func(cmd *cobra.Command, args []string) {
		clientID := strings.TrimSpace(adminRotateClientID)
		if clientID == "" {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin rotate", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--client-id is required")
			return
		}
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin rotate", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		resp, err := ac.RotateClientKey(clientID)
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin rotate", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("rotating client key: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin rotate", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "rotated_client", clientID)
		printKV(cmd.OutOrStdout(), "glm_client_id", strings.TrimSpace(resp.ClientID))
		printKV(cmd.OutOrStdout(), "glm_api_key", strings.TrimSpace(resp.APIKey))
	},
}

var toolsAdminDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete/revoke an existing client.",
	Run: func(cmd *cobra.Command, args []string) {
		clientID := strings.TrimSpace(adminDeleteClientID)
		if clientID == "" {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin delete", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--client-id is required")
			return
		}
		if !adminDeleteYes {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin delete", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "refusing delete without --yes")
			return
		}
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin delete", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		if err := ac.DeleteClient(clientID); err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin delete", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("deleting client: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin delete", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "deleted_client", clientID)
	},
}

var toolsAdminPluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "Manage tool plugins via /admin/plugins/* endpoints.",
}

var toolsAdminPluginsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed plugins.",
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins list", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		plugins, err := ac.ListPlugins()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins list", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("listing plugins: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins list", "success")
		printSection(cmd.OutOrStdout(), "results")
		printKV(cmd.OutOrStdout(), "plugin_count", fmt.Sprintf("%d", len(plugins)))
		if len(plugins) == 0 {
			return
		}
		printSection(cmd.OutOrStdout(), "plugins")
		for _, p := range plugins {
			state := "disabled"
			if p.Enabled {
				state = "enabled"
			}
			name := strings.TrimSpace(p.Name)
			if name == "" {
				name = strings.TrimSpace(p.ID)
			}
			line := "- " + name + " [" + state + "]"
			if pub := strings.TrimSpace(p.Publisher); pub != "" {
				line += " publisher=" + pub
			}
			if id := strings.TrimSpace(p.ID); id != "" {
				line += " id=" + id
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
	},
}

var toolsAdminPluginsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install a plugin.",
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins install", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		req := tools.AdminPluginInstallRequest{
			PluginID: strings.TrimSpace(adminPluginID),
			Source:   strings.TrimSpace(adminPluginSource),
		}
		if strings.TrimSpace(adminPluginManifestPath) != "" {
			b, err := os.ReadFile(strings.TrimSpace(adminPluginManifestPath))
			if err != nil {
				printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins install", "error")
				printSection(cmd.OutOrStdout(), "error")
				printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("reading manifest file: %v", err))
				return
			}
			var manifest map[string]interface{}
			if err := json.Unmarshal(b, &manifest); err != nil {
				printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins install", "error")
				printSection(cmd.OutOrStdout(), "error")
				printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("parsing manifest JSON: %v", err))
				return
			}
			req.Manifest = manifest
		}
		if strings.TrimSpace(req.PluginID) == "" && len(req.Manifest) == 0 {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins install", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--plugin-id or --manifest-file is required")
			return
		}
		resp, err := ac.InstallPlugin(req)
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins install", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("installing plugin: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins install", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "plugin_id", strings.TrimSpace(resp.PluginID))
		printKV(cmd.OutOrStdout(), "status", strings.TrimSpace(resp.Status))
	},
}

var toolsAdminPluginsEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable a plugin by ID.",
	Run: func(cmd *cobra.Command, args []string) {
		pluginID := strings.TrimSpace(adminPluginID)
		if pluginID == "" {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins enable", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--plugin-id is required")
			return
		}
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins enable", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		if err := ac.EnablePlugin(pluginID); err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins enable", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("enabling plugin: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins enable", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "enabled_plugin", pluginID)
	},
}

var toolsAdminPluginsDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable a plugin by ID.",
	Run: func(cmd *cobra.Command, args []string) {
		pluginID := strings.TrimSpace(adminPluginID)
		if pluginID == "" {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins disable", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--plugin-id is required")
			return
		}
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins disable", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		if err := ac.DisablePlugin(pluginID); err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins disable", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("disabling plugin: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin plugins disable", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "disabled_plugin", pluginID)
	},
}

var toolsAdminToolgenCmd = &cobra.Command{
	Use:   "toolgen",
	Short: "Generate plugins via async admin job endpoints.",
}

var toolsAdminToolgenGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Start async plugin generation (POST /admin/plugins).",
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen generate", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		if strings.TrimSpace(adminToolgenName) == "" {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen generate", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--name is required")
			return
		}
		req := tools.AdminToolgenRequest{
			Name:        strings.TrimSpace(adminToolgenName),
			Description: strings.TrimSpace(adminToolgenDescription),
			Publisher:   strings.TrimSpace(adminToolgenPublisher),
			Goal:        strings.TrimSpace(adminToolgenGoal),
			Requirement: strings.TrimSpace(adminToolgenRequirement),
		}
		if strings.TrimSpace(adminToolgenMetadata) != "" {
			var meta map[string]interface{}
			if err := json.Unmarshal([]byte(adminToolgenMetadata), &meta); err != nil {
				printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen generate", "error")
				printSection(cmd.OutOrStdout(), "error")
				printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("parsing --metadata JSON: %v", err))
				return
			}
			req.Metadata = meta
		}
		resp, err := ac.GenerateToolPlugin(req)
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen generate", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("generating plugin: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen generate", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "job_id", strings.TrimSpace(resp.JobID))
		printKV(cmd.OutOrStdout(), "plugin_id", strings.TrimSpace(resp.PluginID))
		printKV(cmd.OutOrStdout(), "name", strings.TrimSpace(resp.Name))
		printKV(cmd.OutOrStdout(), "publisher", strings.TrimSpace(resp.Publisher))
		printKV(cmd.OutOrStdout(), "status", strings.TrimSpace(resp.Status))
		printKV(cmd.OutOrStdout(), "endpoint", strings.TrimSpace(resp.Endpoint))
	},
}

var toolsAdminToolgenStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get toolgen job status by job ID (GET /admin/plugins/jobs/{id}).",
	Run: func(cmd *cobra.Command, args []string) {
		jobID := strings.TrimSpace(adminToolgenJobID)
		if jobID == "" {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen status", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--job-id is required")
			return
		}
		ac, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen status", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing admin client: %v", err))
			return
		}
		resp, err := ac.GetToolgenJob(jobID)
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen status", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("fetching toolgen job: %v", err))
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tools admin toolgen status", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "job_id", strings.TrimSpace(resp.JobID))
		printKV(cmd.OutOrStdout(), "plugin_id", strings.TrimSpace(resp.PluginID))
		printKV(cmd.OutOrStdout(), "name", strings.TrimSpace(resp.Name))
		printKV(cmd.OutOrStdout(), "publisher", strings.TrimSpace(resp.Publisher))
		printKV(cmd.OutOrStdout(), "status", strings.TrimSpace(resp.Status))
		printKV(cmd.OutOrStdout(), "endpoint", strings.TrimSpace(resp.Endpoint))
	},
}

func init() {
	toolsAdminCreateCmd.Flags().StringVar(&adminCreateClientID, "client-id", "", "Optional client ID (auto-generated when omitted)")
	toolsAdminRotateCmd.Flags().StringVar(&adminRotateClientID, "client-id", "", "Client ID to rotate")
	toolsAdminDeleteCmd.Flags().StringVar(&adminDeleteClientID, "client-id", "", "Client ID to delete")
	toolsAdminDeleteCmd.Flags().BoolVar(&adminDeleteYes, "yes", false, "Confirm deletion")
	toolsAdminPluginsInstallCmd.Flags().StringVar(&adminPluginID, "plugin-id", "", "Plugin ID")
	toolsAdminPluginsInstallCmd.Flags().StringVar(&adminPluginSource, "source", "", "Plugin source (optional)")
	toolsAdminPluginsInstallCmd.Flags().StringVar(&adminPluginManifestPath, "manifest-file", "", "Path to manifest JSON (optional)")
	toolsAdminPluginsEnableCmd.Flags().StringVar(&adminPluginID, "plugin-id", "", "Plugin ID")
	toolsAdminPluginsDisableCmd.Flags().StringVar(&adminPluginID, "plugin-id", "", "Plugin ID")
	toolsAdminToolgenGenerateCmd.Flags().StringVar(&adminToolgenName, "name", "", "Plugin/tool name")
	toolsAdminToolgenGenerateCmd.Flags().StringVar(&adminToolgenDescription, "description", "", "Description")
	toolsAdminToolgenGenerateCmd.Flags().StringVar(&adminToolgenPublisher, "publisher", "", "Publisher ID for trusted auto-enable policy")
	toolsAdminToolgenGenerateCmd.Flags().StringVar(&adminToolgenGoal, "goal", "", "Generation goal")
	toolsAdminToolgenGenerateCmd.Flags().StringVar(&adminToolgenRequirement, "requirement", "", "Tool requirement")
	toolsAdminToolgenGenerateCmd.Flags().StringVar(&adminToolgenMetadata, "metadata", "", "Metadata JSON object")
	toolsAdminToolgenStatusCmd.Flags().StringVar(&adminToolgenJobID, "job-id", "", "Toolgen job ID")

	toolsAdminCmd.AddCommand(toolsAdminListCmd)
	toolsAdminCmd.AddCommand(toolsAdminCreateCmd)
	toolsAdminCmd.AddCommand(toolsAdminRotateCmd)
	toolsAdminCmd.AddCommand(toolsAdminDeleteCmd)
	toolsAdminPluginsCmd.AddCommand(toolsAdminPluginsListCmd)
	toolsAdminPluginsCmd.AddCommand(toolsAdminPluginsInstallCmd)
	toolsAdminPluginsCmd.AddCommand(toolsAdminPluginsEnableCmd)
	toolsAdminPluginsCmd.AddCommand(toolsAdminPluginsDisableCmd)
	toolsAdminToolgenCmd.AddCommand(toolsAdminToolgenGenerateCmd)
	toolsAdminToolgenCmd.AddCommand(toolsAdminToolgenStatusCmd)
	toolsAdminCmd.AddCommand(toolsAdminPluginsCmd)
	toolsAdminCmd.AddCommand(toolsAdminToolgenCmd)

	toolsCmd.AddCommand(toolsAdminCmd)
	rootCmd.AddCommand(toolsCmd)
}
