package skills

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/business"
	"github.com/Thynaptic/P-LMv1/pkg/cognition"
)

const (
	defaultComplianceBacklogPath = ".memory/compliance/backlog.jsonl"
	defaultDashboardDurationMS   = 5200
)

var shellKVRE = regexp.MustCompile(`(?m)^([A-Z_]+)=(.+)$`)

// DashboardMetrics is the live status tuple shown in the top-right HUD panel.
type DashboardMetrics struct {
	SovereignScore  int     `json:"sovereign_score"` // out of 100
	ReclaimedHours  float64 `json:"reclaimed_hours"`
	ActiveThreats   int     `json:"active_threats"`
	SecurityPosture string  `json:"security_posture"`
}

// DashboardPanel describes rendered panel bounds on screen.
type DashboardPanel struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// DashboardRenderRequest controls panel placement and content generation.
type DashboardRenderRequest struct {
	Consent      bool
	WorkOrder    cognition.ProductWorkOrder
	DurationMS   int
	ScreenWidth  int
	ScreenHeight int
}

// DashboardRenderResult returns HUD overlay metadata and panel bounds.
type DashboardRenderResult struct {
	Panel     DashboardPanel    `json:"panel"`
	Metrics   DashboardMetrics  `json:"metrics"`
	HUD       DrawWarRoomResult `json:"hud"`
	ProductID string            `json:"product_id"`
	PanelText string            `json:"panel_text"`
}

type complianceBacklogRow struct {
	Timestamp time.Time `json:"timestamp"`
	Findings  []struct {
		Severity string `json:"severity"`
	} `json:"findings,omitempty"`
}

// BuildDashboardMetrics aggregates Sovereign Score, Reclaimed Time, and Active Threats.
func BuildDashboardMetrics(wo cognition.ProductWorkOrder) (DashboardMetrics, error) {
	score := cognition.EvaluateSovereignScore(wo)
	roi, err := business.CalculateDailyROI()
	if err != nil && !os.IsNotExist(err) {
		return DashboardMetrics{}, err
	}
	threats, terr := CountActiveThreats(defaultComplianceBacklogPath, 24*time.Hour)
	if terr != nil && !os.IsNotExist(terr) {
		return DashboardMetrics{}, terr
	}
	return DashboardMetrics{
		SovereignScore:  int(score.Score * 100),
		ReclaimedHours:  roi.TotalReclaimed.Hours(),
		ActiveThreats:   threats,
		SecurityPosture: deriveSecurityPosture(threats),
	}, nil
}

// RenderDashboardHUD draws a high-contrast status panel in the top-right workspace region.
func RenderDashboardHUD(req DashboardRenderRequest) (DashboardRenderResult, error) {
	metrics, err := BuildDashboardMetrics(req.WorkOrder)
	if err != nil {
		return DashboardRenderResult{}, err
	}
	sw, sh := req.ScreenWidth, req.ScreenHeight
	if sw <= 0 || sh <= 0 {
		ws, hs, derr := getDisplayGeometry()
		if derr == nil {
			sw, sh = ws, hs
		}
	}
	if sw <= 0 {
		sw = 1920
	}
	if sh <= 0 {
		sh = 1080
	}
	panel := DashboardPanel{
		W: 340,
		H: 116,
		X: sw - 360,
		Y: 20,
	}
	if panel.X < 20 {
		panel.X = 20
	}

	duration := req.DurationMS
	if duration <= 0 {
		duration = defaultDashboardDurationMS
	}
	panelText := fmt.Sprintf("Sovereign: %d/100\nReclaimed: +%.1f hrs\nSecurity: %s",
		metrics.SovereignScore, metrics.ReclaimedHours, metrics.SecurityPosture)
	productID := fmt.Sprintf("Thynaptic Panel | Threats: %d", metrics.ActiveThreats)

	hud := DrawWarRoom(DrawWarRoomRequest{
		Consent:    req.Consent,
		DurationMS: duration,
		Boxes: []WarRoomBox{
			// Thynaptic Green/Black style: bright green outer frame + dark core frame.
			{X: panel.X, Y: panel.Y, W: panel.W, H: panel.H, Text: panelText, Kind: "entity", Color: "#13f287"},
			{X: panel.X + 4, Y: panel.Y + 4, W: panel.W - 8, H: panel.H - 8, Text: "", Kind: "entity", Color: "#0b0f12"},
			{X: panel.X + 10, Y: panel.Y + panel.H - 34, W: 210, H: 24, Text: productID, Kind: "verified", Color: "#13f287"},
		},
	})

	return DashboardRenderResult{
		Panel:     panel,
		Metrics:   metrics,
		HUD:       hud,
		ProductID: productID,
		PanelText: panelText,
	}, nil
}

// GetCursorPosition reads cursor coordinates using xdotool getmouselocation --shell.
func GetCursorPosition() (x int, y int, err error) {
	if _, lerr := exec.LookPath("xdotool"); lerr != nil {
		return 0, 0, fmt.Errorf("xdotool not available for cursor lookup")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "xdotool", "getmouselocation", "--shell")
	out, runErr := cmd.Output()
	if runErr != nil {
		return 0, 0, runErr
	}
	m := map[string]string{}
	for _, mm := range shellKVRE.FindAllStringSubmatch(string(out), -1) {
		if len(mm) == 3 {
			m[strings.TrimSpace(mm[1])] = strings.TrimSpace(mm[2])
		}
	}
	xv, xerr := strconv.Atoi(strings.TrimSpace(m["X"]))
	yv, yerr := strconv.Atoi(strings.TrimSpace(m["Y"]))
	if xerr != nil || yerr != nil {
		return 0, 0, fmt.Errorf("failed to parse cursor coordinates")
	}
	return xv, yv, nil
}

// ShouldExpandExecutiveReview checks whether the cursor sits inside the dashboard panel.
func ShouldExpandExecutiveReview(panel DashboardPanel) (bool, error) {
	x, y, err := GetCursorPosition()
	if err != nil {
		return false, err
	}
	return pointInPanel(x, y, panel), nil
}

// BuildExecutiveReview expands the Morning Brief into a full executive summary string.
func BuildExecutiveReview(metrics DashboardMetrics) string {
	return strings.TrimSpace(fmt.Sprintf(
		"Executive Review\n- Sovereign Score: %d/100\n- Reclaimed Time: +%.1f hrs\n- Security Posture: %s (%d active threats)\n- Summary: Swarm is holding local-first posture while running maintenance and compliance hardening in parallel.",
		metrics.SovereignScore,
		metrics.ReclaimedHours,
		metrics.SecurityPosture,
		metrics.ActiveThreats,
	))
}

// DetectDashboardFocusBySpatialVision checks whether the top-right dashboard is in active visual focus.
// It uses capture_screen OCR + spatial element hits inside the panel area as a spatial-vision signal.
func DetectDashboardFocusBySpatialVision(panel DashboardPanel, consent bool) (bool, string, error) {
	cap := CaptureScreen(CaptureScreenRequest{
		Consent:        consent,
		Mode:           "screen",
		TimeoutSeconds: 12,
		MaxBase64Chars: 200_000,
	})
	if cap.ExitCode != 0 || !cap.Allowed {
		return false, "", fmt.Errorf("spatial vision capture failed: %s", strings.TrimSpace(cap.Stderr))
	}
	var hits int
	for _, el := range cap.SpatialElements {
		if !pointInPanel(el.X, el.Y, panel) {
			continue
		}
		lbl := strings.ToLower(strings.TrimSpace(el.Label))
		if containsAny(lbl, "sovereign", "reclaimed", "security", "threat", "thynaptic") {
			hits++
		}
	}
	if hits == 0 {
		ocr := strings.ToLower(strings.TrimSpace(cap.OCRText))
		if containsAny(ocr, "sovereign", "reclaimed", "security", "threat") {
			hits = 1
		}
	}
	return hits > 0, fmt.Sprintf("spatial_vision_hits=%d", hits), nil
}

// ExpandExecutiveReviewIfFocused triggers Executive Review narration when panel focus is detected.
func ExpandExecutiveReviewIfFocused(panel DashboardPanel, metrics DashboardMetrics, consent bool) (bool, string, error) {
	focused, reason, err := DetectDashboardFocusBySpatialVision(panel, consent)
	if err != nil {
		return false, "", err
	}
	if !focused {
		return false, "", nil
	}
	review := BuildExecutiveReview(metrics)
	return true, "Mirror Trigger (" + reason + ")\n" + review, nil
}

// CountActiveThreats counts high-severity compliance findings within the lookback window.
func CountActiveThreats(backlogPath string, lookback time.Duration) (int, error) {
	if strings.TrimSpace(backlogPath) == "" {
		backlogPath = defaultComplianceBacklogPath
	}
	f, err := os.Open(backlogPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	if lookback <= 0 {
		lookback = 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-lookback)
	total := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var row complianceBacklogRow
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		if !row.Timestamp.IsZero() && row.Timestamp.Before(cutoff) {
			continue
		}
		for _, fd := range row.Findings {
			if strings.EqualFold(strings.TrimSpace(fd.Severity), "high") {
				total++
			}
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return total, nil
}

func getDisplayGeometry() (int, int, error) {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "xdotool", "getdisplaygeometry")
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid display geometry output")
	}
	w, werr := strconv.Atoi(parts[0])
	h, herr := strconv.Atoi(parts[1])
	if werr != nil || herr != nil {
		return 0, 0, fmt.Errorf("invalid display geometry values")
	}
	return w, h, nil
}

func pointInPanel(x, y int, p DashboardPanel) bool {
	return x >= p.X && x <= p.X+p.W && y >= p.Y && y <= p.Y+p.H
}

func clampScore(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func deriveSecurityPosture(activeThreats int) string {
	switch {
	case activeThreats <= 0:
		return "Hardened"
	case activeThreats <= 2:
		return "Watch"
	default:
		return "Elevated"
	}
}

func containsAny(s string, needles ...string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, n := range needles {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(n))) {
			return true
		}
	}
	return false
}
