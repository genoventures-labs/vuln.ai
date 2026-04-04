package taloscli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func TestNamespaceShowFlagDisplaysPersistedNamespace(t *testing.T) {
	prevFactory := namespaceManagerFactory
	prevRequestedNamespace := requestedNamespace
	prevShowFlag := namespaceShowFlag
	prevClearFlag := namespaceClearFlag
	defer func() {
		namespaceManagerFactory = prevFactory
		requestedNamespace = prevRequestedNamespace
		namespaceShowFlag = prevShowFlag
		namespaceClearFlag = prevClearFlag
	}()

	statePath := filepath.Join(t.TempDir(), "session_state.json")
	smPath, err := state.NewManagerWithPath(statePath)
	if err != nil {
		t.Fatalf("state manager path: %v", err)
	}
	smPath.SetActiveNamespace("talos-runtime")
	if err := smPath.Save(); err != nil {
		t.Fatalf("save state path: %v", err)
	}
	namespaceManagerFactory = func() (*state.Manager, error) {
		return state.NewManagerWithPath(statePath)
	}
	requestedNamespace = ""
	namespaceShowFlag = false
	namespaceClearFlag = false

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"namespace", "--show"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("namespace --show failed: %v", err)
	}
	if !strings.Contains(out.String(), "namespace: talos-runtime") {
		t.Fatalf("expected namespace output, got: %s", out.String())
	}
}

func TestNamespaceBindToolAndBindings(t *testing.T) {
	prevManagerFactory := namespaceManagerFactory
	prevStoreFactory := namespaceCapabilityStoreFactory
	prevRequestedNamespace := requestedNamespace
	prevShowFlag := namespaceShowFlag
	prevClearFlag := namespaceClearFlag
	defer func() {
		namespaceManagerFactory = prevManagerFactory
		namespaceCapabilityStoreFactory = prevStoreFactory
		requestedNamespace = prevRequestedNamespace
		namespaceShowFlag = prevShowFlag
		namespaceClearFlag = prevClearFlag
	}()

	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "session_state.json")
	storePath := filepath.Join(tmp, "namespace_capabilities.json")

	sm, err := state.NewManagerWithPath(statePath)
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	sm.SetActiveNamespace("talos-runtime")
	if err := sm.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}
	namespaceManagerFactory = func() (*state.Manager, error) { return state.NewManagerWithPath(statePath) }
	namespaceCapabilityStoreFactory = func() (*state.NamespaceCapabilityStore, error) {
		return state.NewNamespaceCapabilityStoreWithPath(storePath)
	}
	requestedNamespace = ""
	namespaceShowFlag = false
	namespaceClearFlag = false

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"namespace", "bind-tool", "--id", "web_search"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("namespace bind-tool failed: %v", err)
	}
	if !strings.Contains(out.String(), "bound_tool: web_search") || !strings.Contains(out.String(), "namespace: talos-runtime") {
		t.Fatalf("expected bind confirmation, got: %s", out.String())
	}

	out.Reset()
	rootCmd.SetArgs([]string{"namespace", "bindings"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("namespace bindings failed: %v", err)
	}
	if !strings.Contains(out.String(), "TOOLS") || !strings.Contains(out.String(), "- web_search") {
		t.Fatalf("expected web_search binding in output, got: %s", out.String())
	}
}

func TestNamespaceActivateViaPositionalArgument(t *testing.T) {
	prevFactory := namespaceManagerFactory
	prevShowFlag := namespaceShowFlag
	prevClearFlag := namespaceClearFlag
	defer func() {
		namespaceManagerFactory = prevFactory
		namespaceShowFlag = prevShowFlag
		namespaceClearFlag = prevClearFlag
	}()

	statePath := filepath.Join(t.TempDir(), "session_state.json")
	namespaceManagerFactory = func() (*state.Manager, error) {
		return state.NewManagerWithPath(statePath)
	}
	namespaceShowFlag = false
	namespaceClearFlag = false

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"namespace", "training"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("namespace positional activation failed: %v", err)
	}
	if !strings.Contains(out.String(), "namespace_active: training") {
		t.Fatalf("expected activation output, got: %s", out.String())
	}

	sm, err := state.NewManagerWithPath(statePath)
	if err != nil {
		t.Fatalf("reload state manager: %v", err)
	}
	if got := sm.ActiveNamespace(); got != "training" {
		t.Fatalf("expected persisted namespace training, got %q", got)
	}
}

func TestNamespaceListShowsActiveAndInactive(t *testing.T) {
	prevManagerFactory := namespaceManagerFactory
	prevStoreFactory := namespaceCapabilityStoreFactory
	prevShowFlag := namespaceShowFlag
	prevClearFlag := namespaceClearFlag
	defer func() {
		namespaceManagerFactory = prevManagerFactory
		namespaceCapabilityStoreFactory = prevStoreFactory
		namespaceShowFlag = prevShowFlag
		namespaceClearFlag = prevClearFlag
	}()

	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "session_state.json")
	storePath := filepath.Join(tmp, "namespace_capabilities.json")

	sm, err := state.NewManagerWithPath(statePath)
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	sm.SetActiveNamespace("training")
	if err := sm.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}
	namespaceManagerFactory = func() (*state.Manager, error) { return state.NewManagerWithPath(statePath) }

	store, err := state.NewNamespaceCapabilityStoreWithPath(storePath)
	if err != nil {
		t.Fatalf("capability store: %v", err)
	}
	store.AllowTool("talos-runtime", "web_search")
	if err := store.Save(); err != nil {
		t.Fatalf("save store: %v", err)
	}
	namespaceCapabilityStoreFactory = func() (*state.NamespaceCapabilityStore, error) {
		return state.NewNamespaceCapabilityStoreWithPath(storePath)
	}
	namespaceShowFlag = false
	namespaceClearFlag = false

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"namespace", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("namespace list failed: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "- training [active]") {
		t.Fatalf("expected active namespace in list, got: %s", rendered)
	}
	if !strings.Contains(rendered, "- talos-runtime [inactive]") {
		t.Fatalf("expected inactive namespace in list, got: %s", rendered)
	}
}
