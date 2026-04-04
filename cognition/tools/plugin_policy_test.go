package tools

import "testing"

func TestIsTrustedPlugin_Allowlist(t *testing.T) {
	t.Setenv("GLM_TRUSTED_PLUGIN_PUBLISHERS", "acme, thynaptic")
	t.Setenv("GLM_TRUSTED_PLUGIN_PREFIXES", "talos-,trusted_")

	if !IsTrustedPlugin(TrustedPluginMetadata{Publisher: "thynaptic"}) {
		t.Fatalf("expected trusted publisher to pass")
	}
	if !IsTrustedPlugin(TrustedPluginMetadata{ID: "talos-netops"}) {
		t.Fatalf("expected trusted prefix ID to pass")
	}
	if !IsTrustedPlugin(TrustedPluginMetadata{Name: "trusted_parser"}) {
		t.Fatalf("expected trusted prefix name to pass")
	}
	if IsTrustedPlugin(TrustedPluginMetadata{Publisher: "unknown", ID: "x", Name: "y"}) {
		t.Fatalf("expected unknown plugin to fail")
	}
}
