package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultHUDConsentStatePath = ".memory/hud_consent.json"

type overlayConsentState struct {
	SessionID string    `json:"session_id"`
	Scope     string    `json:"scope"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func HasActiveOverlayConsent(sessionID string, scope string) bool {
	sessionID = strings.TrimSpace(sessionID)
	scope = strings.TrimSpace(strings.ToLower(scope))
	if sessionID == "" || scope == "" {
		return false
	}
	st, err := readOverlayConsentState(defaultHUDConsentStatePath)
	if err != nil {
		return false
	}
	if strings.TrimSpace(st.SessionID) != sessionID {
		return false
	}
	if time.Now().After(st.ExpiresAt) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(st.Scope)) {
	case "all":
		return true
	case scope:
		return true
	default:
		return false
	}
}

func GrantOverlayConsent(sessionID string, scope string, ttl time.Duration) error {
	sessionID = strings.TrimSpace(sessionID)
	scope = strings.TrimSpace(strings.ToLower(scope))
	if sessionID == "" || scope == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	st := overlayConsentState{
		SessionID: sessionID,
		Scope:     scope,
		GrantedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	return writeOverlayConsentState(defaultHUDConsentStatePath, st)
}

func RevokeOverlayConsent(sessionID string) error {
	st, err := readOverlayConsentState(defaultHUDConsentStatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(st.SessionID) != strings.TrimSpace(sessionID) {
		return nil
	}
	return os.Remove(defaultHUDConsentStatePath)
}

func readOverlayConsentState(path string) (overlayConsentState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return overlayConsentState{}, err
	}
	var st overlayConsentState
	if err := json.Unmarshal(raw, &st); err != nil {
		return overlayConsentState{}, err
	}
	return st, nil
}

func writeOverlayConsentState(path string, st overlayConsentState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
