package tools

import (
	"os"
	"strings"
)

const (
	trustedPluginPublishersEnv = "GLM_TRUSTED_PLUGIN_PUBLISHERS"
	trustedPluginPrefixesEnv   = "GLM_TRUSTED_PLUGIN_PREFIXES"
)

// TrustedPluginMetadata is the minimal identity set used by trust policy checks.
type TrustedPluginMetadata struct {
	ID        string
	Name      string
	Publisher string
}

// IsTrustedPlugin checks env-driven allowlists for publisher and plugin ID/name prefixes.
func IsTrustedPlugin(meta TrustedPluginMetadata) bool {
	publisher := strings.ToLower(strings.TrimSpace(meta.Publisher))
	id := strings.ToLower(strings.TrimSpace(meta.ID))
	name := strings.ToLower(strings.TrimSpace(meta.Name))

	for _, allowed := range splitCSVEnv(trustedPluginPublishersEnv) {
		if publisher != "" && publisher == allowed {
			return true
		}
	}
	for _, prefix := range splitCSVEnv(trustedPluginPrefixesEnv) {
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(id, prefix) || strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func splitCSVEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.ToLower(strings.TrimSpace(p))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
