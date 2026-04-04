package skills

import (
	"os"
	"testing"
	"time"
)

func TestOverlayConsentGrantAndExpiry(t *testing.T) {
	_ = os.Remove(defaultHUDConsentStatePath)
	t.Cleanup(func() { _ = os.Remove(defaultHUDConsentStatePath) })

	if err := GrantOverlayConsent("session-a", "all", 50*time.Millisecond); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	if !HasActiveOverlayConsent("session-a", "draw_box") {
		t.Fatal("expected active consent")
	}
	time.Sleep(70 * time.Millisecond)
	if HasActiveOverlayConsent("session-a", "draw_box") {
		t.Fatal("expected expired consent")
	}
}
