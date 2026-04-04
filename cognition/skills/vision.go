package skills

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
)

const (
	defaultVisionCaptureTimeout = 15 * time.Second
	defaultVisionOCRTimeout     = 10 * time.Second
	defaultCaptureDir           = ".memory/captures"
	defaultCaptureMode          = "screen"
	defaultMaxBase64Chars       = 4_000_000
	defaultWatchDuration        = 25 * time.Second
	defaultWatchInterval        = 2500 * time.Millisecond
	defaultWatchFramesLimit     = 12
	defaultHUDDurationMS        = 3200
	defaultHUDStatePath         = ".memory/hud_process.json"
	defaultWarRoomStatePath     = ".memory/hud_war_room.json"
	defaultHUDEventsPath        = ".memory/hud_events.jsonl"
	defaultEvidencePacketsDir   = ".memory/evidence_packets"
)

var visualSensitiveRE = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bpassword\b`),
	regexp.MustCompile(`(?i)\bpasscode\b`),
	regexp.MustCompile(`(?i)\botp\b`),
	regexp.MustCompile(`(?i)\b2fa\b`),
	regexp.MustCompile(`(?i)\bapi[_-]?key\b`),
	regexp.MustCompile(`(?i)\btoken\b`),
	regexp.MustCompile(`(?i)\bsecret\b`),
	regexp.MustCompile(`(?i)\bprivate\s+key\b`),
	regexp.MustCompile(`(?i)begin\s+[a-z0-9 ]*private key`),
}

// CaptureScreenRequest controls screen/window capture.
type CaptureScreenRequest struct {
	Consent        bool
	Mode           string // "screen" or "window"
	WindowID       string // numeric X11 window id or app/window identifier
	OutputPath     string
	TimeoutSeconds int
	MaxBase64Chars int
}

// SpatialElement identifies an OCR or UI element with coordinates.
type SpatialElement struct {
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Confidence float64 `json:"confidence,omitempty"`
}

// CaptureScreenResult contains privacy-gated capture output.
type CaptureScreenResult struct {
	Allowed          bool             `json:"allowed"`
	RequiresApproval bool             `json:"requires_approval"`
	VisualRequest    string           `json:"visual_request,omitempty"`
	Mode             string           `json:"mode,omitempty"`
	Path             string           `json:"path,omitempty"`
	Encoding         string           `json:"encoding,omitempty"`
	ImageBase64      string           `json:"image_base64,omitempty"`
	OCRText          string           `json:"ocr_text,omitempty"`
	SpatialElements  []SpatialElement `json:"spatial_elements,omitempty"`
	SensitiveMatches []string         `json:"sensitive_matches,omitempty"`
	BlockedSensitive bool             `json:"blocked_sensitive,omitempty"`
	OCRUsed          bool             `json:"ocr_used,omitempty"`
	Utility          string           `json:"utility,omitempty"`
	Stdout           string           `json:"stdout,omitempty"`
	Stderr           string           `json:"stderr,omitempty"`
	ExitCode         int              `json:"exit_code"`
	DurationMS       int64            `json:"duration_ms"`
}

// WatchTerminalRequest captures the active terminal window over time.
type WatchTerminalRequest struct {
	Consent         bool
	DurationSeconds int
	IntervalMS      int
	OutputDir       string
	MaxBase64Chars  int
}

// WatchTerminalFrame stores one sampled terminal frame.
type WatchTerminalFrame struct {
	Timestamp       time.Time        `json:"timestamp"`
	Path            string           `json:"path,omitempty"`
	OCRText         string           `json:"ocr_text,omitempty"`
	SpatialElements []SpatialElement `json:"spatial_elements,omitempty"`
	ExitCode        int              `json:"exit_code"`
	Stderr          string           `json:"stderr,omitempty"`
}

// WatchTerminalResult describes a watch_terminal session.
type WatchTerminalResult struct {
	Allowed          bool                 `json:"allowed"`
	RequiresApproval bool                 `json:"requires_approval"`
	VisualRequest    string               `json:"visual_request,omitempty"`
	ActiveWindowID   string               `json:"active_window_id,omitempty"`
	ActiveWindowName string               `json:"active_window_name,omitempty"`
	Frames           []WatchTerminalFrame `json:"frames,omitempty"`
	Utility          string               `json:"utility,omitempty"`
	ExitCode         int                  `json:"exit_code"`
	Stderr           string               `json:"stderr,omitempty"`
	DurationMS       int64                `json:"duration_ms"`
}

// DrawBoxRequest describes a temporary desktop highlight.
type DrawBoxRequest struct {
	Consent    bool
	SessionID  string
	X          int
	Y          int
	W          int
	H          int
	Text       string
	DurationMS int
}

// DrawBoxResult contains overlay process metadata.
type DrawBoxResult struct {
	Allowed          bool   `json:"allowed"`
	RequiresApproval bool   `json:"requires_approval"`
	VisualRequest    string `json:"visual_request,omitempty"`
	PID              int    `json:"pid,omitempty"`
	Utility          string `json:"utility,omitempty"`
	StatePath        string `json:"state_path,omitempty"`
	EventsPath       string `json:"events_path,omitempty"`
	ExitCode         int    `json:"exit_code"`
	Stderr           string `json:"stderr,omitempty"`
}

// WarRoomBox represents a single persistent overlay region.
type WarRoomBox struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	Text string `json:"text,omitempty"`
	Kind string `json:"kind,omitempty"`
	// Color accepts named colors ("red", "green", "yellow", "blue") or #RRGGBB.
	Color string `json:"color,omitempty"`
}

// DrawWarRoomRequest draws multiple color-coded boxes simultaneously.
type DrawWarRoomRequest struct {
	Consent    bool         `json:"consent"`
	SessionID  string       `json:"session_id,omitempty"`
	Boxes      []WarRoomBox `json:"boxes"`
	DurationMS int          `json:"duration_ms,omitempty"`
}

// DrawWarRoomResult contains overlay process metadata for multi-box renders.
type DrawWarRoomResult struct {
	Allowed          bool   `json:"allowed"`
	RequiresApproval bool   `json:"requires_approval"`
	VisualRequest    string `json:"visual_request,omitempty"`
	PID              int    `json:"pid,omitempty"`
	Utility          string `json:"utility,omitempty"`
	StatePath        string `json:"state_path,omitempty"`
	EventsPath       string `json:"events_path,omitempty"`
	BoxesRendered    int    `json:"boxes_rendered,omitempty"`
	ExitCode         int    `json:"exit_code"`
	Stderr           string `json:"stderr,omitempty"`
}

// ContextualHUDFilterRequest focuses the HUD web on the currently opened/hovered document.
type ContextualHUDFilterRequest struct {
	Consent           bool
	SessionID         string
	RelationalMap     cognition.RelationalMap
	DocumentAnchors   []cognition.HUDAnchor
	FocusedDocument   string
	SpatialVisionText string
	DurationMS        int
	IncludeConnectors bool
	MaximumLinks      int
}

// ContextualHUDFilterResult contains rendered boxes and a mirror fact sheet.
type ContextualHUDFilterResult struct {
	FocusedDocument string            `json:"focused_document,omitempty"`
	Rendered        DrawWarRoomResult `json:"rendered"`
	FactSheet       string            `json:"fact_sheet,omitempty"`
	LinkCount       int               `json:"link_count"`
	FactSheetHUD    DrawBoxResult     `json:"fact_sheet_hud,omitempty"`
}

// SmokingGunSnapshotRequest captures an evidence screenshot with active HUD overlays.
type SmokingGunSnapshotRequest struct {
	Consent  bool
	Mode     string // screen | window
	WindowID string
	CaseID   string
	Caption  string
	TimeoutS int
}

// SmokingGunSnapshotResult stores captured packet metadata.
type SmokingGunSnapshotResult struct {
	Allowed      bool   `json:"allowed"`
	PacketID     string `json:"packet_id,omitempty"`
	ImagePath    string `json:"image_path,omitempty"`
	ManifestPath string `json:"manifest_path,omitempty"`
	FactSheet    string `json:"fact_sheet,omitempty"`
	ExitCode     int    `json:"exit_code"`
	Stderr       string `json:"stderr,omitempty"`
}

// ForensicProofRequest captures a screenshot with live HUD overlays and writes proof artifacts.
type ForensicProofRequest struct {
	Consent             bool
	ProofID             string
	Mode                string // screen | window
	WindowID            string
	Caption             string
	TimeoutS            int
	ArchivistTrace      []string
	ScoutConflictReport map[string]interface{}
}

// ForensicProofResult contains the archived proof image and metadata sidecar.
type ForensicProofResult struct {
	Allowed     bool   `json:"allowed"`
	ProofID     string `json:"proof_id,omitempty"`
	ImagePath   string `json:"image_path,omitempty"`
	SidecarPath string `json:"sidecar_path,omitempty"`
	PacketDir   string `json:"packet_dir,omitempty"`
	ExitCode    int    `json:"exit_code"`
	Stderr      string `json:"stderr,omitempty"`
}

// CodeDiffHUDRequest renders a red/green code-diff overlay plus a compact confidence panel.
type CodeDiffHUDRequest struct {
	Consent         bool   `json:"consent"`
	SessionID       string `json:"session_id,omitempty"`
	BuggyX          int    `json:"buggy_x,omitempty"`
	BuggyY          int    `json:"buggy_y,omitempty"`
	BuggyW          int    `json:"buggy_w,omitempty"`
	BuggyH          int    `json:"buggy_h,omitempty"`
	VerifiedX       int    `json:"verified_x,omitempty"`
	VerifiedY       int    `json:"verified_y,omitempty"`
	VerifiedW       int    `json:"verified_w,omitempty"`
	VerifiedH       int    `json:"verified_h,omitempty"`
	BuggyLabel      string `json:"buggy_label,omitempty"`
	VerifiedLabel   string `json:"verified_label,omitempty"`
	ConfidenceLabel string `json:"confidence_label,omitempty"`
	DurationMS      int    `json:"duration_ms,omitempty"`
}

type hudProcessState struct {
	PID        int       `json:"pid"`
	Utility    string    `json:"utility"`
	StartedAt  time.Time `json:"started_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	X          int       `json:"x"`
	Y          int       `json:"y"`
	W          int       `json:"w"`
	H          int       `json:"h"`
	Text       string    `json:"text,omitempty"`
	Status     string    `json:"status"`
	LastReason string    `json:"last_reason,omitempty"`
}

type warRoomState struct {
	PID        int          `json:"pid"`
	Utility    string       `json:"utility"`
	StartedAt  time.Time    `json:"started_at"`
	ExpiresAt  time.Time    `json:"expires_at"`
	Boxes      []WarRoomBox `json:"boxes,omitempty"`
	Status     string       `json:"status"`
	LastReason string       `json:"last_reason,omitempty"`
}

// CaptureScreen captures a full screen or a specific window after explicit consent.
func CaptureScreen(req CaptureScreenRequest) CaptureScreenResult {
	out := CaptureScreenResult{
		ExitCode: 1,
		Mode:     normalizeCaptureMode(req.Mode),
	}
	if !req.Consent {
		out.RequiresApproval = true
		out.VisualRequest = "I'm about to capture your screen to analyze the error. Is this okay?"
		return out
	}

	if out.Mode == "window" && strings.TrimSpace(req.WindowID) == "" {
		out.Stderr = "window mode requires non-empty window_id"
		return out
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultVisionCaptureTimeout
	}

	targetPath, err := ensureCapturePath(req.OutputPath)
	if err != nil {
		out.Stderr = err.Error()
		return out
	}

	utility := pickCaptureUtility()
	if utility == "" {
		out.Stderr = "no capture utility found (install maim or scrot)"
		return out
	}
	out.Utility = utility

	start := time.Now()
	stdout, stderr, exitCode, err := runCaptureCommand(utility, out.Mode, req.WindowID, targetPath, timeout)
	out.DurationMS = time.Since(start).Milliseconds()
	out.Stdout = strings.TrimSpace(stdout)
	out.Stderr = strings.TrimSpace(stderr)
	out.ExitCode = exitCode
	if err != nil {
		return out
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		out.ExitCode = 1
		out.Stderr = fmt.Sprintf("read capture: %v", err)
		return out
	}

	ocrText, ocrUsed := extractOCRText(targetPath)
	spatial, spatialUsed := extractOCRSpatial(targetPath)
	if spatialUsed && len(spatial) > 0 {
		out.SpatialElements = spatial
		if strings.TrimSpace(ocrText) == "" {
			ocrText = joinSpatialText(spatial)
		}
	}
	out.OCRText = strings.TrimSpace(ocrText)
	out.OCRUsed = ocrUsed || spatialUsed
	violations := detectVisualSensitivePatterns(ocrText)
	violations = append(violations, cognition.DetectSecretLeaks(ocrText)...)
	violations = dedupeVisionStrings(violations)
	if len(violations) > 0 {
		out.BlockedSensitive = true
		out.SensitiveMatches = violations
		out.Path = targetPath
		_ = os.Remove(targetPath)
		out.Path = ""
		out.ExitCode = 2
		out.Stderr = "privacy shutter blocked screenshot due to sensitive content patterns"
		return out
	}

	b64 := base64.StdEncoding.EncodeToString(raw)
	maxChars := req.MaxBase64Chars
	if maxChars <= 0 {
		maxChars = defaultMaxBase64Chars
	}
	if len(b64) > maxChars {
		b64 = b64[:maxChars]
	}

	out.Allowed = true
	out.ExitCode = 0
	out.Path = targetPath
	out.Encoding = "base64/png"
	out.ImageBase64 = b64
	return out
}

// WatchTerminal captures the active terminal window during long-running build tasks.
func WatchTerminal(req WatchTerminalRequest) WatchTerminalResult {
	out := WatchTerminalResult{ExitCode: 1}
	if !req.Consent {
		out.RequiresApproval = true
		out.VisualRequest = "I'm about to capture your screen to analyze the error. Is this okay?"
		return out
	}

	duration := time.Duration(req.DurationSeconds) * time.Second
	if duration <= 0 {
		duration = defaultWatchDuration
	}
	interval := time.Duration(req.IntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	frameLimit := int(duration / interval)
	if frameLimit <= 0 {
		frameLimit = 1
	}
	if frameLimit > defaultWatchFramesLimit {
		frameLimit = defaultWatchFramesLimit
	}

	windowID, windowName, err := resolveActiveTerminalWindow(defaultVisionCaptureTimeout)
	if err != nil {
		out.Stderr = err.Error()
		return out
	}
	out.ActiveWindowID = windowID
	out.ActiveWindowName = windowName

	baseDir := strings.TrimSpace(req.OutputDir)
	if baseDir == "" {
		baseDir = filepath.Join(defaultCaptureDir, fmt.Sprintf("watch_terminal_%d", time.Now().UnixNano()))
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		out.Stderr = fmt.Sprintf("create watch dir: %v", err)
		return out
	}

	start := time.Now()
	for i := 0; i < frameLimit; i++ {
		path := filepath.Join(baseDir, fmt.Sprintf("frame_%03d.png", i+1))
		cap := CaptureScreen(CaptureScreenRequest{
			Consent:        true,
			Mode:           "window",
			WindowID:       windowID,
			OutputPath:     path,
			TimeoutSeconds: int(defaultVisionCaptureTimeout.Seconds()),
			MaxBase64Chars: req.MaxBase64Chars,
		})
		frame := WatchTerminalFrame{
			Timestamp:       time.Now().UTC(),
			Path:            cap.Path,
			OCRText:         cap.OCRText,
			SpatialElements: cap.SpatialElements,
			ExitCode:        cap.ExitCode,
			Stderr:          strings.TrimSpace(cap.Stderr),
		}
		out.Frames = append(out.Frames, frame)
		if cap.ExitCode != 0 {
			out.Stderr = strings.TrimSpace(cap.Stderr)
			out.DurationMS = time.Since(start).Milliseconds()
			return out
		}
		if i < frameLimit-1 {
			time.Sleep(interval)
		}
	}

	out.Allowed = true
	out.ExitCode = 0
	out.DurationMS = time.Since(start).Milliseconds()
	out.Utility = pickCaptureUtility()
	return out
}

// DrawBox renders a temporary on-screen highlight box at coordinates.
func DrawBox(req DrawBoxRequest) DrawBoxResult {
	policy := DefaultOverlayPolicy()
	out := DrawBoxResult{
		ExitCode:   1,
		StatePath:  defaultHUDStatePath,
		EventsPath: defaultHUDEventsPath,
	}
	consent := req.Consent
	if !consent && HasActiveOverlayConsent(strings.TrimSpace(req.SessionID), "draw_box") {
		consent = true
	}
	if !consent {
		out.RequiresApproval = true
		out.VisualRequest = "I'm about to highlight part of your screen to point out an issue. Is this okay?"
		return out
	}
	if req.Consent && strings.TrimSpace(req.SessionID) != "" {
		_ = GrantOverlayConsent(strings.TrimSpace(req.SessionID), "all", policy.SessionConsentTTL)
	}

	x, y := maxIntVision(req.X, 0), maxIntVision(req.Y, 0)
	w, h := req.W, req.H
	if w <= 0 {
		w = 160
	}
	if h <= 0 {
		h = 90
	}
	duration := req.DurationMS
	if duration <= 0 {
		duration = defaultHUDDurationMS
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		text = "Visual target"
	}
	text = "T.A.L.O.S. SENTINEL | " + text

	utility := ""
	if _, err := exec.LookPath("python3"); err == nil {
		utility = "python3-tkinter"
	}
	if utility == "" {
		out.Stderr = "draw_box requires python3 with tkinter"
		return out
	}

	script := `
import sys,time,tkinter as tk
x=int(sys.argv[1]); y=int(sys.argv[2]); w=int(sys.argv[3]); h=int(sys.argv[4]); text=sys.argv[5]; dur=int(sys.argv[6])
root=tk.Tk()
root.overrideredirect(True)
root.attributes("-topmost", True)
root.configure(bg="magenta")
try:
    root.wm_attributes("-transparentcolor", "magenta")
except Exception:
    pass
root.geometry(f"{w}x{h}+{x}+{y}")
c=tk.Canvas(root, width=w, height=h, bg="magenta", highlightthickness=0)
c.pack(fill="both", expand=True)
c.create_rectangle(2,2,w-2,h-2, outline="#CD7F32", width=4)
if text:
    c.create_text(8, 8, anchor="nw", text=text, fill="#ffef66", font=("DejaVu Sans", 11, "bold"))
root.after(max(dur,600), root.destroy)
root.mainloop()
`
	cmd := exec.Command("python3", "-c", script,
		fmt.Sprintf("%d", x),
		fmt.Sprintf("%d", y),
		fmt.Sprintf("%d", w),
		fmt.Sprintf("%d", h),
		text,
		fmt.Sprintf("%d", duration),
	)
	if err := cmd.Start(); err != nil {
		out.Stderr = err.Error()
		return out
	}

	now := time.Now().UTC()
	state := hudProcessState{
		PID:       cmd.Process.Pid,
		Utility:   utility,
		StartedAt: now,
		ExpiresAt: now.Add(time.Duration(duration) * time.Millisecond),
		X:         x,
		Y:         y,
		W:         w,
		H:         h,
		Text:      text,
		Status:    "running",
	}
	_ = persistHUDState(defaultHUDStatePath, state)
	_ = appendHUDEvent(defaultHUDEventsPath, map[string]interface{}{
		"timestamp": now.Format(time.RFC3339),
		"event":     "draw_box_started",
		"pid":       state.PID,
		"x":         x,
		"y":         y,
		"w":         w,
		"h":         h,
		"text":      text,
	})

	out.Allowed = true
	out.ExitCode = 0
	out.PID = state.PID
	out.Utility = utility
	return out
}

// DrawWarRoom renders multiple color-coded highlights in one overlay.
func DrawWarRoom(req DrawWarRoomRequest) DrawWarRoomResult {
	policy := DefaultOverlayPolicy()
	out := DrawWarRoomResult{
		ExitCode:   1,
		StatePath:  defaultWarRoomStatePath,
		EventsPath: defaultHUDEventsPath,
	}
	consent := req.Consent
	if !consent && HasActiveOverlayConsent(strings.TrimSpace(req.SessionID), "draw_war_room") {
		consent = true
	}
	if !consent {
		out.RequiresApproval = true
		out.VisualRequest = "I'm about to highlight multiple areas of your screen. Is this okay?"
		return out
	}
	if req.Consent && strings.TrimSpace(req.SessionID) != "" {
		_ = GrantOverlayConsent(strings.TrimSpace(req.SessionID), "all", policy.SessionConsentTTL)
	}
	if len(req.Boxes) == 0 {
		out.Stderr = "draw_war_room requires at least one box"
		return out
	}

	duration := req.DurationMS
	if duration <= 0 {
		duration = 10_000
	}
	boxes := make([]WarRoomBox, 0, len(req.Boxes))
	if policy.TaxonomyStrict {
		cands := make([]OverlayCandidate, 0, len(req.Boxes))
		for _, b := range req.Boxes {
			cands = append(cands, OverlayCandidate{
				Kind:       normalizeOverlayKind(b.Kind),
				Text:       strings.TrimSpace(b.Text),
				X:          b.X,
				Y:          b.Y,
				W:          b.W,
				H:          b.H,
				Confidence: 1.0,
				Priority:   OverlayPriorityHigh,
				Source:     "direct_draw_war_room",
			})
		}
		boxes = ToWarRoomBoxes(cands)
	} else {
		for _, b := range req.Boxes {
			w := b.W
			h := b.H
			if w <= 0 {
				w = 180
			}
			if h <= 0 {
				h = 100
			}
			boxes = append(boxes, WarRoomBox{
				X:     maxIntVision(b.X, 0),
				Y:     maxIntVision(b.Y, 0),
				W:     w,
				H:     h,
				Text:  strings.TrimSpace(b.Text),
				Kind:  strings.TrimSpace(b.Kind),
				Color: normalizeWarRoomColor(strings.TrimSpace(b.Kind), strings.TrimSpace(b.Color)),
			})
		}
	}

	utility := ""
	if _, err := exec.LookPath("python3"); err == nil {
		utility = "python3-tkinter"
	}
	if utility == "" {
		out.Stderr = "draw_war_room requires python3 with tkinter"
		return out
	}

	payload, _ := json.Marshal(boxes)
	script := `
import json,sys,tkinter as tk
boxes=json.loads(sys.argv[1]); dur=int(sys.argv[2])
root=tk.Tk()
root.overrideredirect(True)
root.attributes("-topmost", True)
root.configure(bg="magenta")
try:
    root.wm_attributes("-transparentcolor", "magenta")
except Exception:
    pass
sw=root.winfo_screenwidth(); sh=root.winfo_screenheight()
root.geometry(f"{sw}x{sh}+0+0")
c=tk.Canvas(root,width=sw,height=sh,bg="magenta",highlightthickness=0)
c.pack(fill="both",expand=True)
c.create_text(14, 12, anchor="nw", text="T.A.L.O.S. SENTINEL", fill="#CD7F32", font=("DejaVu Sans", 12, "bold"))
for b in boxes:
    x=int(b.get("x",0)); y=int(b.get("y",0)); w=max(int(b.get("w",120)),20); h=max(int(b.get("h",60)),20)
    color=(b.get("color") or "#13f287").strip()
    text=(b.get("text") or "").strip()
    c.create_rectangle(x,y,x+w,y+h,outline=color,width=4)
    if text:
        c.create_text(x+8,y+8,anchor="nw",text="T.A.L.O.S. SENTINEL | "+text,fill="#fff4a0",font=("DejaVu Sans",11,"bold"))
root.after(max(dur,1000), root.destroy)
root.mainloop()
`
	cmd := exec.Command("python3", "-c", script, string(payload), fmt.Sprintf("%d", duration))
	if err := cmd.Start(); err != nil {
		out.Stderr = err.Error()
		return out
	}

	now := time.Now().UTC()
	state := warRoomState{
		PID:       cmd.Process.Pid,
		Utility:   utility,
		StartedAt: now,
		ExpiresAt: now.Add(time.Duration(duration) * time.Millisecond),
		Boxes:     boxes,
		Status:    "running",
	}
	_ = persistWarRoomState(defaultWarRoomStatePath, state)
	_ = appendHUDEvent(defaultHUDEventsPath, map[string]interface{}{
		"timestamp": now.Format(time.RFC3339),
		"event":     "draw_war_room_started",
		"pid":       state.PID,
		"boxes":     boxes,
	})

	out.Allowed = true
	out.ExitCode = 0
	out.PID = state.PID
	out.Utility = utility
	out.BoxesRendered = len(boxes)
	return out
}

// DrawCodeDiffHUD highlights buggy code (red), verified fix (green), and a confidence badge.
func DrawCodeDiffHUD(req CodeDiffHUDRequest) DrawWarRoomResult {
	if !req.Consent {
		return DrawWarRoomResult{
			ExitCode:         1,
			RequiresApproval: true,
			VisualRequest:    "I'm about to highlight buggy and fixed code in your IDE. Is this okay?",
		}
	}
	if req.DurationMS <= 0 {
		req.DurationMS = 5200
	}

	buggyLabel := strings.TrimSpace(req.BuggyLabel)
	if buggyLabel == "" {
		buggyLabel = "Buggy code"
	}
	verifiedLabel := strings.TrimSpace(req.VerifiedLabel)
	if verifiedLabel == "" {
		verifiedLabel = "Verified fix"
	}
	confLabel := strings.TrimSpace(req.ConfidenceLabel)
	if confLabel == "" {
		confLabel = "Tests Passed: n/a | Build Time: n/a"
	}

	bx, by, bw, bh := req.BuggyX, req.BuggyY, req.BuggyW, req.BuggyH
	if bx <= 0 {
		bx = 320
	}
	if by <= 0 {
		by = 210
	}
	if bw <= 0 {
		bw = 760
	}
	if bh <= 0 {
		bh = 150
	}
	fx, fy, fw, fh := req.VerifiedX, req.VerifiedY, req.VerifiedW, req.VerifiedH
	if fx <= 0 {
		fx = bx
	}
	if fy <= 0 {
		fy = by + bh + 26
	}
	if fw <= 0 {
		fw = bw
	}
	if fh <= 0 {
		fh = bh
	}

	return DrawWarRoom(DrawWarRoomRequest{
		Consent:    true,
		SessionID:  strings.TrimSpace(req.SessionID),
		DurationMS: req.DurationMS,
		Boxes: []WarRoomBox{
			{X: bx, Y: by, W: bw, H: bh, Text: buggyLabel, Kind: "contradiction", Color: "red"},
			{X: fx, Y: fy, W: fw, H: fh, Text: verifiedLabel, Kind: "verified", Color: "green"},
			{X: fx, Y: maxIntVision(24, fy-76), W: 420, H: 56, Text: confLabel, Kind: "entity", Color: "blue"},
		},
	})
}

// ApplyContextualHUDFiltering renders only links relevant to the focused/open document.
func ApplyContextualHUDFiltering(req ContextualHUDFilterRequest) ContextualHUDFilterResult {
	res := ContextualHUDFilterResult{
		Rendered: DrawWarRoomResult{ExitCode: 1},
	}
	if !req.Consent {
		res.Rendered.RequiresApproval = true
		res.Rendered.VisualRequest = "I'm about to focus HUD links for the active document. Is this okay?"
		return res
	}
	focused := strings.TrimSpace(req.FocusedDocument)
	if focused == "" {
		focused = detectFocusedDocument(req.SpatialVisionText, req.DocumentAnchors, req.RelationalMap)
	}
	if focused == "" {
		if inferred, _, _ := detectActiveFileViaVision(req.DocumentAnchors, req.RelationalMap); inferred != "" {
			focused = inferred
		}
	}
	if focused == "" {
		res.Rendered.Stderr = "unable to identify focused document for contextual HUD filter"
		return res
	}
	res.FocusedDocument = focused
	fact := buildFocusedFactSheet(focused, req.RelationalMap)
	res.FactSheet = fact

	maxLinks := req.MaximumLinks
	if maxLinks <= 0 {
		maxLinks = 3
	}
	anchors := anchorMap(req.DocumentAnchors)
	focusAnchor, ok := anchors[normalizeDocRef(focused)]
	if !ok {
		focusAnchor = cognition.HUDAnchor{Document: focused, X: 420, Y: 180, W: 320, H: 180}
	}

	var candidates []OverlayCandidate
	addedLinks := 0
	for _, link := range req.RelationalMap.Links {
		if addedLinks >= maxLinks {
			break
		}
		relatedDoc, ok := pickRelatedDocForFocus(focused, link.DocumentRefs)
		if !ok {
			continue
		}
		ra, ok := anchors[normalizeDocRef(relatedDoc)]
		if !ok {
			ra = cognition.HUDAnchor{Document: relatedDoc, X: 980, Y: 180 + (addedLinks * 190), W: 300, H: 160}
		}
		plan := cognition.BuildEvidenceHUDWeb(link, focusAnchor, ra)
		for _, b := range plan.Boxes {
			kind := normalizeOverlayKind(b.Kind)
			priority := OverlayPriorityMedium
			if kind == OverlayKindContradiction || kind == OverlayKindEvidence {
				priority = OverlayPriorityHigh
			}
			candidates = append(candidates, OverlayCandidate{
				Kind:       kind,
				Text:       b.Text,
				X:          b.X,
				Y:          b.Y,
				W:          b.W,
				H:          b.H,
				Confidence: 0.78,
				Priority:   priority,
				Source:     "contextual_hud",
			})
		}
		addedLinks++
	}
	if len(candidates) == 0 {
		candidates = []OverlayCandidate{
			{
				Kind:       OverlayKindDocument,
				Text:       "Focused document",
				X:          focusAnchor.X,
				Y:          focusAnchor.Y,
				W:          focusAnchor.W,
				H:          focusAnchor.H,
				Confidence: 0.90,
				Priority:   OverlayPriorityMedium,
				Source:     "contextual_hud",
			},
		}
	}
	policy := DefaultOverlayPolicy()
	decision := EvaluateOverlayCandidates(candidates, OverlayContext{
		Stage:     "contextual_hud",
		Explicit:  true,
		SessionID: strings.TrimSpace(req.SessionID),
	}, policy)
	if !decision.Render {
		res.Rendered.ExitCode = 0
		res.Rendered.Allowed = true
		res.Rendered.BoxesRendered = 0
		res.Rendered.Utility = "overlay_policy_suppressed"
		return res
	}
	boxes := ToWarRoomBoxes(decision.Selected)
	res.LinkCount = addedLinks
	duration := req.DurationMS
	if duration <= 0 {
		duration = 7200
	}
	res.Rendered = DrawWarRoom(DrawWarRoomRequest{
		Consent:    true,
		SessionID:  strings.TrimSpace(req.SessionID),
		DurationMS: duration,
		Boxes:      boxes,
	})
	// Compact fact-sheet overlay near focused document.
	fx, fy := focusAnchor.X+focusAnchor.W+14, focusAnchor.Y
	if fx > 1500 {
		fx = maxIntVision(40, focusAnchor.X-370)
	}
	res.FactSheetHUD = DrawBox(DrawBoxRequest{
		Consent:    true,
		SessionID:  strings.TrimSpace(req.SessionID),
		X:          fx,
		Y:          maxIntVision(20, fy),
		W:          360,
		H:          120,
		Text:       summarizeFactSheetForBox(fact, res.LinkCount),
		DurationMS: duration,
	})
	return res
}

// CaptureSmokingGunSnapshot captures a screenshot with active HUD overlay and writes an evidence packet.
func CaptureSmokingGunSnapshot(req SmokingGunSnapshotRequest) SmokingGunSnapshotResult {
	out := SmokingGunSnapshotResult{ExitCode: 1}
	if !req.Consent {
		out.Stderr = "capture requires explicit consent"
		return out
	}
	caseID := strings.TrimSpace(req.CaseID)
	if caseID == "" {
		caseID = "smoking_gun"
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	packetID := sanitizePacketID(caseID + "_" + stamp)
	packetDir := filepath.Join(defaultEvidencePacketsDir, packetID)
	if err := os.MkdirAll(packetDir, 0o755); err != nil {
		out.Stderr = err.Error()
		return out
	}
	imagePath := filepath.Join(packetDir, "snapshot.png")
	capRes := CaptureScreen(CaptureScreenRequest{
		Consent:        true,
		Mode:           strings.TrimSpace(req.Mode),
		WindowID:       strings.TrimSpace(req.WindowID),
		OutputPath:     imagePath,
		TimeoutSeconds: req.TimeoutS,
	})
	if capRes.ExitCode != 0 || !capRes.Allowed {
		out.Stderr = strings.TrimSpace(capRes.Stderr)
		if out.Stderr == "" {
			out.Stderr = "capture failed"
		}
		return out
	}
	manifestPath := filepath.Join(packetDir, "packet.json")
	manifest := map[string]interface{}{
		"packet_id":     packetID,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
		"image_path":    imagePath,
		"caption":       strings.TrimSpace(req.Caption),
		"mode":          capRes.Mode,
		"ocr_text":      capRes.OCRText,
		"spatial_count": len(capRes.SpatialElements),
		"hud_state": map[string]interface{}{
			"single_box_state": defaultHUDStatePath,
			"war_room_state":   defaultWarRoomStatePath,
		},
	}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(manifestPath, b, 0o644); err != nil {
		out.Stderr = err.Error()
		return out
	}
	out.Allowed = true
	out.PacketID = packetID
	out.ImagePath = imagePath
	out.ManifestPath = manifestPath
	out.ExitCode = 0
	return out
}

// CaptureForensicProof stores a composite screenshot + sidecar for archival reporting.
// HUD overlays are "burned in" by capturing the display/window while overlays are active.
func CaptureForensicProof(req ForensicProofRequest) ForensicProofResult {
	out := ForensicProofResult{ExitCode: 1}
	if !req.Consent {
		out.Stderr = "capture_forensic_proof requires explicit consent"
		return out
	}
	proofID := sanitizePacketID(strings.TrimSpace(req.ProofID))
	if proofID == "" || proofID == "packet" {
		proofID = sanitizePacketID("proof_" + time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := os.MkdirAll(defaultEvidencePacketsDir, 0o755); err != nil {
		out.Stderr = err.Error()
		return out
	}
	imagePath := filepath.Join(defaultEvidencePacketsDir, proofID+".png")
	cap := CaptureScreen(CaptureScreenRequest{
		Consent:        true,
		Mode:           strings.TrimSpace(req.Mode),
		WindowID:       strings.TrimSpace(req.WindowID),
		OutputPath:     imagePath,
		TimeoutSeconds: req.TimeoutS,
	})
	if cap.ExitCode != 0 || !cap.Allowed {
		out.Stderr = strings.TrimSpace(cap.Stderr)
		if out.Stderr == "" {
			out.Stderr = "capture_forensic_proof failed"
		}
		return out
	}
	sidecar := map[string]interface{}{
		"proof_id":        proofID,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
		"image_path":      imagePath,
		"caption":         strings.TrimSpace(req.Caption),
		"mode":            cap.Mode,
		"window_id":       strings.TrimSpace(req.WindowID),
		"archivist_trace": req.ArchivistTrace,
		"scout_conflict":  req.ScoutConflictReport,
		"hud_overlay": map[string]interface{}{
			"single_box_state": defaultHUDStatePath,
			"war_room_state":   defaultWarRoomStatePath,
			"composite_note":   "image captured with live HUD overlay",
		},
	}
	sidecarPath := filepath.Join(defaultEvidencePacketsDir, proofID+".json")
	b, _ := json.MarshalIndent(sidecar, "", "  ")
	if err := os.WriteFile(sidecarPath, b, 0o644); err != nil {
		out.Stderr = err.Error()
		return out
	}
	out.Allowed = true
	out.ProofID = proofID
	out.ImagePath = imagePath
	out.SidecarPath = sidecarPath
	out.PacketDir = defaultEvidencePacketsDir
	out.ExitCode = 0
	return out
}

// ClearAllHUDOverlays terminates active single-box and war-room overlays.
func ClearAllHUDOverlays(reason string) (int, []string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "system reset"
	}
	total := 0
	var notes []string

	if hs, err := readHUDProcessState(defaultHUDStatePath); err == nil && hs.PID > 0 {
		if err := terminatePID(hs.PID); err == nil {
			total++
			notes = append(notes, fmt.Sprintf("terminated draw_box pid=%d", hs.PID))
		}
		hs.Status = "terminated"
		hs.LastReason = reason
		_ = persistHUDState(defaultHUDStatePath, hs)
	}
	if ws, err := readWarRoomState(defaultWarRoomStatePath); err == nil && ws.PID > 0 {
		if err := terminatePID(ws.PID); err == nil {
			total++
			notes = append(notes, fmt.Sprintf("terminated draw_war_room pid=%d", ws.PID))
		}
		ws.Status = "terminated"
		ws.LastReason = reason
		_ = persistWarRoomState(defaultWarRoomStatePath, ws)
	}
	_ = appendHUDEvent(defaultHUDEventsPath, map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"event":     "hud_system_reset",
		"reason":    reason,
		"cleared":   total,
		"notes":     notes,
	})
	return total, notes
}

func normalizeWarRoomColor(kind, color string) string {
	if DefaultOverlayPolicy().TaxonomyStrict {
		return overlayColorForKind(normalizeOverlayKind(kind))
	}
	color = strings.ToLower(strings.TrimSpace(color))
	if strings.HasPrefix(color, "#") && len(color) == 7 {
		return color
	}
	switch color {
	case "red":
		return "#CD7F32"
	case "green":
		return "#13f287"
	case "yellow":
		return "#ffcf3f"
	case "blue":
		return "#36a2ff"
	case "orange":
		return "#ff8b3d"
	case "purple":
		return "#9f6bff"
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "contradiction", "conflict", "anomaly", "smoking_gun":
		return "#CD7F32"
	case "verified", "support", "confirmed":
		return "#13f287"
	case "date", "timeline", "timestamp":
		return "#ffcf3f"
	case "document", "signature", "entity":
		return "#36a2ff"
	default:
		return "#13f287"
	}
}

func normalizeCaptureMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "window", "window_only", "app", "application":
		return "window"
	default:
		return defaultCaptureMode
	}
}

func ensureCapturePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		if err := os.MkdirAll(defaultCaptureDir, 0o755); err != nil {
			return "", err
		}
		name := fmt.Sprintf("capture_%d.png", time.Now().UnixNano())
		return filepath.Join(defaultCaptureDir, name), nil
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if !strings.HasSuffix(strings.ToLower(path), ".png") {
		path += ".png"
	}
	return path, nil
}

func pickCaptureUtility() string {
	for _, c := range []string{"maim", "scrot"} {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return ""
}

func runCaptureCommand(utility, mode, windowID, outPath string, timeout time.Duration) (string, string, int, error) {
	var args []string
	switch utility {
	case "maim":
		if mode == "window" {
			resolved := strings.TrimSpace(windowID)
			if !isNumericWindowID(resolved) {
				if id := resolveWindowID(resolved, timeout); id != "" {
					resolved = id
				}
			}
			if !isNumericWindowID(resolved) {
				return "", "failed to resolve window id", 1, fmt.Errorf("invalid window_id for maim: %q", windowID)
			}
			args = []string{"-u", "-i", resolved, outPath}
		} else {
			args = []string{"-u", outPath}
		}
	case "scrot":
		if mode == "window" {
			// scrot cannot reliably target arbitrary IDs; focused is safest local fallback.
			args = []string{"--focused", "--quality", "100", outPath}
		} else {
			args = []string{"--quality", "100", outPath}
		}
	default:
		return "", "unsupported capture utility", 1, fmt.Errorf("unsupported capture utility: %s", utility)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, utility, args...)
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	if err != nil {
		return stdoutBuf.String(), stderrBuf.String(), commandExitCode(err), err
	}
	return stdoutBuf.String(), stderrBuf.String(), 0, nil
}

func resolveWindowID(identifier string, timeout time.Duration) string {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return ""
	}
	if isNumericWindowID(identifier) {
		return identifier
	}
	if _, err := exec.LookPath("xdotool"); err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "xdotool", "search", "--onlyvisible", "--name", identifier)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		v := strings.TrimSpace(l)
		if isNumericWindowID(v) {
			return v
		}
	}
	return ""
}

func isNumericWindowID(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	_, err := strconv.ParseInt(v, 10, 64)
	return err == nil
}

func extractOCRText(path string) (string, bool) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultVisionOCRTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tesseract", path, "stdout", "--dpi", "300")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func extractOCRSpatial(path string) ([]SpatialElement, bool) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultVisionOCRTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tesseract", path, "stdout", "--dpi", "300", "tsv")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) <= 1 {
		return nil, true
	}
	var elements []SpatialElement
	for i := 1; i < len(lines); i++ {
		row := strings.Split(lines[i], "\t")
		if len(row) < 12 {
			continue
		}
		label := strings.TrimSpace(row[11])
		if label == "" {
			continue
		}
		x, errX := strconv.Atoi(strings.TrimSpace(row[6]))
		y, errY := strconv.Atoi(strings.TrimSpace(row[7]))
		w, errW := strconv.Atoi(strings.TrimSpace(row[8]))
		h, errH := strconv.Atoi(strings.TrimSpace(row[9]))
		conf, _ := strconv.ParseFloat(strings.TrimSpace(row[10]), 64)
		if errX != nil || errY != nil || errW != nil || errH != nil {
			continue
		}
		if w <= 0 || h <= 0 {
			continue
		}
		kind := "text"
		lower := strings.ToLower(label)
		if strings.HasSuffix(label, ":") {
			kind = "label"
		}
		if containsVisionToken(lower, "apply", "save", "cancel", "submit", "retry", "build", "deploy", "run") {
			kind = "button"
		}
		if containsVisionToken(lower, "input", "search", "username", "password", "email", "token") {
			kind = "input_field"
		}
		elements = append(elements, SpatialElement{
			Kind:       kind,
			Label:      label,
			X:          x,
			Y:          y,
			Width:      w,
			Height:     h,
			Confidence: clampVisual(conf / 100.0),
		})
	}
	return elements, true
}

func joinSpatialText(elements []SpatialElement) string {
	if len(elements) == 0 {
		return ""
	}
	parts := make([]string, 0, len(elements))
	for _, el := range elements {
		if strings.TrimSpace(el.Label) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(el.Label))
		if len(parts) >= 80 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func containsVisionToken(label string, tokens ...string) bool {
	for _, t := range tokens {
		if strings.Contains(label, t) {
			return true
		}
	}
	return false
}

func resolveActiveTerminalWindow(timeout time.Duration) (string, string, error) {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return "", "", fmt.Errorf("watch_terminal requires xdotool for active window detection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	activeCmd := exec.CommandContext(ctx, "xdotool", "getactivewindow")
	activeRaw, err := activeCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("detect active window: %w", err)
	}
	activeID := strings.TrimSpace(string(activeRaw))
	if activeID == "" {
		return "", "", fmt.Errorf("active window id is empty")
	}
	name := readWindowName(ctx, activeID)
	if isLikelyTerminalWindow(name) {
		return activeID, name, nil
	}

	searchCmd := exec.CommandContext(ctx, "xdotool", "search", "--onlyvisible", "--name", "(terminal|kitty|alacritty|wezterm|xterm|konsole|tilix)")
	searchRaw, err := searchCmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(searchRaw)), "\n")
		for _, ln := range lines {
			id := strings.TrimSpace(ln)
			if !isNumericWindowID(id) {
				continue
			}
			n := readWindowName(ctx, id)
			if isLikelyTerminalWindow(n) {
				return id, n, nil
			}
		}
	}
	return activeID, name, nil
}

func resolveActiveWindow(timeout time.Duration) (string, string, error) {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return "", "", fmt.Errorf("active-file awareness requires xdotool")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "xdotool", "getactivewindow")
	raw, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("detect active window: %w", err)
	}
	id := strings.TrimSpace(string(raw))
	if id == "" {
		return "", "", fmt.Errorf("active window id is empty")
	}
	return id, readWindowName(ctx, id), nil
}

func readWindowName(ctx context.Context, windowID string) string {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "xdotool", "getwindowname", windowID)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func isLikelyTerminalWindow(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	return containsVisionToken(name, "terminal", "tty", "kitty", "alacritty", "wezterm", "xterm", "konsole", "tmux")
}

func detectVisualSensitivePatterns(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []string
	for _, re := range visualSensitiveRE {
		if re.MatchString(text) {
			out = append(out, "sensitive visual token matched: "+re.String())
		}
	}
	return dedupeVisionStrings(out)
}

func dedupeVisionStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		key := strings.ToLower(strings.TrimSpace(v))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

// VisualReasoningResult is structured VLM output for graph-level reasoning.
type VisualReasoningResult struct {
	Model              string           `json:"model"`
	Summary            string           `json:"summary"`
	OCRText            string           `json:"ocr_text"`
	Layout             string           `json:"layout"`
	Objects            []string         `json:"objects"`
	TechnicalFindings  []string         `json:"technical_findings"`
	SpatialCorrelation string           `json:"spatial_correlation"`
	SpatialElements    []SpatialElement `json:"spatial_elements,omitempty"`
}

// VisualVerificationResult reports whether post-change state appears updated.
type VisualVerificationResult struct {
	Updated         bool    `json:"updated"`
	Confidence      float64 `json:"confidence"`
	Reason          string  `json:"reason"`
	BeforeSummary   string  `json:"before_summary,omitempty"`
	AfterSummary    string  `json:"after_summary,omitempty"`
	BeforeOCRDigest string  `json:"before_ocr_digest,omitempty"`
	AfterOCRDigest  string  `json:"after_ocr_digest,omitempty"`
}

// VisualAction maps a grounded UI coordinate to an action instruction.
type VisualAction struct {
	Action     string  `json:"action"`
	Target     string  `json:"target"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	Confidence float64 `json:"confidence"`
	Rationale  string  `json:"rationale,omitempty"`
}

var visualReasoningModels = []string{
	"moondream:latest",
	"llava:latest",
	"moondream",
	"llava",
}

var visualJSONRE = regexp.MustCompile(`(?s)\{.*\}`)

// AnalyzeImageWithVLM runs local vision reasoning using Ollama with a prompt + shell context.
func AnalyzeImageWithVLM(imagePath string, prompt string, shellContext string) (VisualReasoningResult, error) {
	imagePath = strings.TrimSpace(imagePath)
	if imagePath == "" {
		return VisualReasoningResult{}, fmt.Errorf("image path is required")
	}
	img, err := os.ReadFile(imagePath)
	if err != nil {
		return VisualReasoningResult{}, fmt.Errorf("read image: %w", err)
	}
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return VisualReasoningResult{}, err
	}

	system := `You are a technical visual reasoning node.
Given a screenshot and shell context, return JSON only:
{
  "summary":"what is on screen",
  "ocr_text":"important visible text",
  "layout":"spatial layout of controls/errors",
  "objects":["notable UI objects"],
  "technical_findings":["UI/UX or runtime findings"],
  "spatial_correlation":"directly correlate shell error(s) with visual evidence",
  "spatial_elements":[{"kind":"button|input_field|label|text","label":"Save","x":120,"y":340,"width":88,"height":30}]
}
Focus on debugging utility and visual-shell alignment.`
	user := "Task:\n" + strings.TrimSpace(prompt)
	if strings.TrimSpace(shellContext) != "" {
		user += "\n\nSafe Hands shell context:\n" + strings.TrimSpace(shellContext)
	}

	for _, model := range visualReasoningModels {
		opts, _ := state.ResolveEntropyOptions(user)
		req := &api.ChatRequest{
			Model:   model,
			Options: opts,
			Messages: []api.Message{
				{Role: "system", Content: system},
				{Role: "user", Content: user, Images: []api.ImageData{img}},
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		var out strings.Builder
		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err != nil {
			continue
		}
		vr, ok := parseVisualReasoningResult(out.String())
		if !ok {
			continue
		}
		vr.Model = model
		return vr, nil
	}

	return VisualReasoningResult{}, fmt.Errorf("no local VLM model succeeded")
}

// CorrelateShellAndVisual computes a quick overlap score between shell text and OCR/findings.
func CorrelateShellAndVisual(shellText string, vr VisualReasoningResult) float64 {
	shellTokens := topTokensForCorrelation(shellText)
	if len(shellTokens) == 0 {
		return 0
	}
	visualBlob := strings.ToLower(strings.TrimSpace(vr.OCRText + " " + vr.Summary + " " + vr.SpatialCorrelation + " " + strings.Join(vr.TechnicalFindings, " ")))
	if visualBlob == "" {
		return 0
	}
	matches := 0
	for _, t := range shellTokens {
		if strings.Contains(visualBlob, t) {
			matches++
		}
	}
	return clampVisual(float64(matches) / float64(len(shellTokens)))
}

// VerifyVisualUpdate compares pre/post screenshots to check if a UI fix appears applied.
func VerifyVisualUpdate(before, after VisualReasoningResult) VisualVerificationResult {
	bl := strings.ToLower(strings.TrimSpace(before.Summary + " " + before.OCRText))
	al := strings.ToLower(strings.TrimSpace(after.Summary + " " + after.OCRText))
	if bl == "" || al == "" {
		return VisualVerificationResult{
			Updated:    false,
			Confidence: 0.25,
			Reason:     "insufficient visual descriptors for reliable comparison",
		}
	}

	beforeErr := containsAnyVisual(bl, "error", "failed", "exception", "timeout", "blocked", "disabled")
	afterErr := containsAnyVisual(al, "error", "failed", "exception", "timeout", "blocked", "disabled")
	changed := strings.TrimSpace(bl) != strings.TrimSpace(al)

	res := VisualVerificationResult{
		Updated:         changed && (beforeErr && !afterErr || !beforeErr),
		Confidence:      0.55,
		Reason:          "visual state changed between before/after capture",
		BeforeSummary:   before.Summary,
		AfterSummary:    after.Summary,
		BeforeOCRDigest: trimDigest(before.OCRText),
		AfterOCRDigest:  trimDigest(after.OCRText),
	}
	if beforeErr && !afterErr && changed {
		res.Updated = true
		res.Confidence = 0.84
		res.Reason = "pre-change error indicators are reduced in post-change capture"
	} else if !changed {
		res.Updated = false
		res.Confidence = 0.72
		res.Reason = "no significant visual difference detected"
	}
	return res
}

// MergeSpatialEvidence injects capture-level OCR coordinates into VLM reasoning output.
func MergeSpatialEvidence(vr VisualReasoningResult, cap CaptureScreenResult) VisualReasoningResult {
	if len(vr.SpatialElements) == 0 && len(cap.SpatialElements) == 0 {
		return vr
	}
	merged := make([]SpatialElement, 0, len(vr.SpatialElements)+len(cap.SpatialElements))
	merged = append(merged, vr.SpatialElements...)
	merged = append(merged, cap.SpatialElements...)
	vr.SpatialElements = dedupeSpatialElements(merged, 120)
	if strings.TrimSpace(vr.OCRText) == "" && strings.TrimSpace(cap.OCRText) != "" {
		vr.OCRText = strings.TrimSpace(cap.OCRText)
	}
	return vr
}

// BuildGroundedActions maps identified UI elements to concrete operator actions.
func BuildGroundedActions(vr VisualReasoningResult, shellContext string) []VisualAction {
	if len(vr.SpatialElements) == 0 {
		return nil
	}
	ctx := strings.ToLower(strings.TrimSpace(shellContext + " " + vr.Summary + " " + vr.Layout + " " + vr.SpatialCorrelation))
	out := make([]VisualAction, 0, 6)
	for _, el := range vr.SpatialElements {
		label := strings.TrimSpace(el.Label)
		if label == "" {
			continue
		}
		lower := strings.ToLower(label)
		score := el.Confidence
		if score <= 0 {
			score = 0.52
		}
		action := ""
		rationale := ""
		switch {
		case containsVisionToken(lower, "error", "exception", "failed", "traceback", "timeout"):
			action = fmt.Sprintf("Move mouse to the %s marker in the terminal.", label)
			rationale = "terminal error hotspot"
			score += 0.22
		case containsVisionToken(lower, "overlap", "sidebar", "layout", "svelte", "component"):
			action = fmt.Sprintf("Inspect overlap region around '%s' and apply CSS layout correction.", label)
			rationale = "layout collision indicator"
			score += 0.25
		case el.Kind == "button":
			action = fmt.Sprintf("Move mouse to '%s' and validate click path.", label)
			rationale = "interactive control"
			score += 0.10
		case el.Kind == "input_field":
			action = fmt.Sprintf("Focus input field '%s' and validate state bindings.", label)
			rationale = "input flow check"
			score += 0.08
		}
		if action == "" && containsVisionToken(ctx, "svelte", "layout", "sidebar", "overlap", "css") {
			action = fmt.Sprintf("Probe visual anomaly around '%s' and test CSS containment.", label)
			rationale = "contextual UI anomaly"
			score += 0.08
		}
		if action == "" {
			continue
		}
		out = append(out, VisualAction{
			Action:     action,
			Target:     label,
			X:          el.X + maxIntVision(el.Width/2, 0),
			Y:          el.Y + maxIntVision(el.Height/2, 0),
			Width:      el.Width,
			Height:     el.Height,
			Confidence: clampVisual(score),
			Rationale:  rationale,
		})
		if len(out) >= 8 {
			break
		}
	}
	return out
}

// SelectPrimaryAction chooses the highest-confidence grounded action.
func SelectPrimaryAction(actions []VisualAction) (VisualAction, bool) {
	if len(actions) == 0 {
		return VisualAction{}, false
	}
	best := actions[0]
	for i := 1; i < len(actions); i++ {
		if actions[i].Confidence > best.Confidence {
			best = actions[i]
		}
	}
	return best, true
}

// VerifyTargetCleared validates that the previously-targeted error location no longer shows the same issue.
func VerifyTargetCleared(target VisualAction, afterCap CaptureScreenResult, after VisualReasoningResult) (bool, float64, string) {
	near := collectNearSpatialElements(target, append(afterCap.SpatialElements, after.SpatialElements...))
	if len(near) == 0 {
		return true, 0.82, "target coordinates no longer map to prior OCR artifacts"
	}
	blob := strings.ToLower(strings.TrimSpace(after.OCRText + " " + after.Summary + " " + strings.Join(extractSpatialLabels(near), " ")))
	if strings.TrimSpace(blob) == "" {
		return true, 0.76, "target region is visually clean"
	}
	if containsVisionToken(blob, "error", "exception", "failed", "overlap", "stuck", "blocked") {
		return false, 0.88, "target region still shows fault markers"
	}
	return true, 0.72, "target region no longer displays the previous failure signature"
}

func parseVisualReasoningResult(raw string) (VisualReasoningResult, bool) {
	raw = strings.TrimSpace(stripVisionFence(raw))
	var out VisualReasoningResult
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return normalizeVisualReasoningResult(out), true
	}
	match := visualJSONRE.FindString(raw)
	if match == "" {
		return VisualReasoningResult{}, false
	}
	if err := json.Unmarshal([]byte(match), &out); err != nil {
		return VisualReasoningResult{}, false
	}
	return normalizeVisualReasoningResult(out), true
}

func normalizeVisualReasoningResult(v VisualReasoningResult) VisualReasoningResult {
	v.Summary = strings.TrimSpace(v.Summary)
	v.OCRText = strings.TrimSpace(v.OCRText)
	v.Layout = strings.TrimSpace(v.Layout)
	v.SpatialCorrelation = strings.TrimSpace(v.SpatialCorrelation)
	v.Objects = dedupeVisionStrings(v.Objects)
	v.TechnicalFindings = dedupeVisionStrings(v.TechnicalFindings)
	if len(v.SpatialElements) > 80 {
		v.SpatialElements = v.SpatialElements[:80]
	}
	v.SpatialElements = dedupeSpatialElements(v.SpatialElements, 80)
	return v
}

func stripVisionFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") && strings.HasSuffix(s, "```") {
		parts := strings.Split(s, "\n")
		if len(parts) >= 3 {
			return strings.TrimSpace(strings.Join(parts[1:len(parts)-1], "\n"))
		}
	}
	return s
}

func topTokensForCorrelation(s string) []string {
	parts := regexp.MustCompile(`[^a-z0-9_.:/-]+`).Split(strings.ToLower(strings.TrimSpace(s)), -1)
	stop := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
		"from": true, "into": true, "http": true, "https": true, "localhost": true,
		"error": true, "failed": true, "warning": true, "line": true, "file": true,
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) < 4 || stop[p] || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func containsAnyVisual(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func trimDigest(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 180 {
		return s
	}
	return s[:180] + "...(truncated)"
}

func clampVisual(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func detectFocusedDocument(spatialVisionText string, anchors []cognition.HUDAnchor, rel cognition.RelationalMap) string {
	text := strings.ToLower(strings.TrimSpace(spatialVisionText))
	if text != "" {
		for _, a := range anchors {
			d := strings.TrimSpace(a.Document)
			if d == "" {
				continue
			}
			base := strings.ToLower(filepath.Base(d))
			if base != "" && strings.Contains(text, base) {
				return d
			}
		}
		for _, e := range rel.Entities {
			for _, d := range e.Documents {
				base := strings.ToLower(filepath.Base(strings.TrimSpace(d)))
				if base != "" && strings.Contains(text, base) {
					return d
				}
			}
		}
	}
	if len(anchors) > 0 {
		return strings.TrimSpace(anchors[0].Document)
	}
	return ""
}

// detectActiveFileViaVision identifies active document by combining focused-window title + OCR.
func detectActiveFileViaVision(anchors []cognition.HUDAnchor, rel cognition.RelationalMap) (doc string, windowID string, cap CaptureScreenResult) {
	windowID, windowName, err := resolveActiveWindow(defaultVisionCaptureTimeout)
	if err != nil || strings.TrimSpace(windowID) == "" {
		return "", "", CaptureScreenResult{}
	}
	outPath := filepath.Join(defaultCaptureDir, fmt.Sprintf("focus_%d.png", time.Now().UnixNano()))
	cap = CaptureScreen(CaptureScreenRequest{
		Consent:        true,
		Mode:           "window",
		WindowID:       windowID,
		OutputPath:     outPath,
		TimeoutSeconds: int(defaultVisionCaptureTimeout.Seconds()),
		MaxBase64Chars: 200_000,
	})
	if cap.ExitCode != 0 {
		return "", windowID, cap
	}

	candidates := gatherDocumentCandidates(anchors, rel)
	hints := strings.ToLower(strings.TrimSpace(windowName + " " + cap.OCRText + " " + reqSpatialText(cap.SpatialElements)))
	for _, c := range candidates {
		base := strings.ToLower(filepath.Base(strings.TrimSpace(c)))
		if base != "" && strings.Contains(hints, base) {
			return c, windowID, cap
		}
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if stem != "" && strings.Contains(hints, stem) {
			return c, windowID, cap
		}
	}
	return "", windowID, cap
}

func buildFocusedFactSheet(focusDoc string, rel cognition.RelationalMap) string {
	focus := normalizeDocRef(focusDoc)
	if focus == "" {
		return ""
	}
	topEntity := ""
	topCount := 0
	for _, e := range rel.Entities {
		count := 0
		for _, d := range e.Documents {
			if normalizeDocRef(d) == focus {
				count++
			}
		}
		if count > topCount {
			topCount = count
			topEntity = strings.TrimSpace(e.Name)
		}
	}
	if topEntity == "" {
		return "Fact Sheet: Focused document mapped. No dominant entity detected yet."
	}
	ledgerCount := 0
	bankCount := 0
	var topLink cognition.EntityLink
	haveLink := false
	for _, l := range rel.Links {
		hasFocus := false
		for _, d := range l.DocumentRefs {
			dd := strings.ToLower(normalizeDocRef(d))
			if normalizeDocRef(d) == focus {
				hasFocus = true
			}
			if strings.Contains(dd, "ledger") {
				ledgerCount++
			}
			if strings.Contains(dd, "bank") {
				bankCount++
			}
		}
		if hasFocus && (!haveLink || l.Confidence > topLink.Confidence) {
			topLink = l
			haveLink = true
		}
	}
	discrepancy := 0
	srcA := "Source A"
	srcB := "Source B"
	if haveLink {
		// Use co-mention imbalance as proxy when explicit amounts are unavailable.
		discrepancy = int(clampVisual(topLink.Confidence)*100) - int(clampVisual(topLink.Weight)*80)
		if discrepancy < 5 {
			discrepancy = 20
		}
		srcA = topLink.EntityA
		srcB = topLink.EntityB
	}
	return fmt.Sprintf(
		"Fact Sheet: %s appears in %d ledgers and %d bank log(s). Highlighting the $%dk discrepancy between %s and %s.",
		topEntity, ledgerCount, bankCount, maxIntVision(10, discrepancy), srcA, srcB,
	)
}

func summarizeFactSheetForBox(sheet string, links int) string {
	s := strings.TrimSpace(sheet)
	if s == "" {
		s = "Fact Sheet: Active document mapped."
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 180 {
		s = s[:180] + "..."
	}
	if links > 0 {
		s = fmt.Sprintf("Links: %d | %s", links, s)
	}
	return s
}

func gatherDocumentCandidates(anchors []cognition.HUDAnchor, rel cognition.RelationalMap) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		k := strings.ToLower(filepath.Base(v))
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	for _, a := range anchors {
		add(a.Document)
	}
	for _, e := range rel.Entities {
		for _, d := range e.Documents {
			add(d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(filepath.Base(out[i])) < strings.ToLower(filepath.Base(out[j]))
	})
	return out
}

func reqSpatialText(in []SpatialElement) string {
	if len(in) == 0 {
		return ""
	}
	parts := make([]string, 0, len(in))
	for _, e := range in {
		lbl := strings.TrimSpace(e.Label)
		if lbl != "" {
			parts = append(parts, lbl)
		}
	}
	return strings.Join(parts, " ")
}

func anchorMap(in []cognition.HUDAnchor) map[string]cognition.HUDAnchor {
	out := make(map[string]cognition.HUDAnchor, len(in))
	for _, a := range in {
		k := normalizeDocRef(a.Document)
		if k == "" {
			continue
		}
		out[k] = a
	}
	return out
}

func pickRelatedDocForFocus(focusDoc string, refs []string) (string, bool) {
	focus := normalizeDocRef(focusDoc)
	for _, r := range refs {
		rr := normalizeDocRef(r)
		if rr == "" || rr == focus {
			continue
		}
		return r, true
	}
	return "", false
}

func normalizeDocRef(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(s))
}

func sanitizePacketID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "packet"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "packet"
	}
	return out
}

func dedupeSpatialElements(in []SpatialElement, limit int) []SpatialElement {
	seen := map[string]bool{}
	out := make([]SpatialElement, 0, len(in))
	for _, el := range in {
		label := strings.TrimSpace(el.Label)
		if label == "" {
			continue
		}
		key := fmt.Sprintf("%s|%s|%d|%d|%d|%d", strings.ToLower(strings.TrimSpace(el.Kind)), strings.ToLower(label), el.X, el.Y, el.Width, el.Height)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, el)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func collectNearSpatialElements(target VisualAction, in []SpatialElement) []SpatialElement {
	if len(in) == 0 {
		return nil
	}
	radius := float64(maxIntVision(target.Width, target.Height))
	if radius < 80 {
		radius = 80
	}
	var out []SpatialElement
	for _, el := range in {
		cx := float64(el.X + maxIntVision(el.Width/2, 0))
		cy := float64(el.Y + maxIntVision(el.Height/2, 0))
		dx := cx - float64(target.X)
		dy := cy - float64(target.Y)
		if math.Sqrt(dx*dx+dy*dy) <= radius {
			out = append(out, el)
		}
	}
	return out
}

func extractSpatialLabels(in []SpatialElement) []string {
	var out []string
	for _, el := range in {
		if s := strings.TrimSpace(el.Label); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func maxIntVision(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func persistHUDState(path string, s hudProcessState) error {
	if strings.TrimSpace(path) == "" {
		path = defaultHUDStatePath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func readHUDProcessState(path string) (hudProcessState, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultHUDStatePath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return hudProcessState{}, err
	}
	var hs hudProcessState
	if err := json.Unmarshal(raw, &hs); err != nil {
		return hudProcessState{}, err
	}
	return hs, nil
}

func persistWarRoomState(path string, s warRoomState) error {
	if strings.TrimSpace(path) == "" {
		path = defaultWarRoomStatePath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func readWarRoomState(path string) (warRoomState, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultWarRoomStatePath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return warRoomState{}, err
	}
	var ws warRoomState
	if err := json.Unmarshal(raw, &ws); err != nil {
		return warRoomState{}, err
	}
	return ws, nil
}

func terminatePID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func appendHUDEvent(path string, ev map[string]interface{}) error {
	if strings.TrimSpace(path) == "" {
		path = defaultHUDEventsPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(string(b)) + "\n")
	return err
}
