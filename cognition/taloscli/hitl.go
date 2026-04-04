package taloscli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	hitlTriggersEnabledEnv = "TALOS_HITL_TRIGGERS_ENABLED"
	hitlInteractiveEnv     = "TALOS_HITL_INTERACTIVE"
	hitlTimeoutSecEnv      = "TALOS_HITL_TIMEOUT_SEC"
	hitlStreamTriggerEnv   = "TALOS_HITL_STREAM_TRIGGER_ENABLED"
)

var (
	hitlInputReader io.Reader = os.Stdin
	hitlOutput      io.Writer = os.Stdout
	hitlPromptMu    sync.Mutex
	hitlIsTTYFn     = isTTYInput
)

type hitlDecision string

const (
	hitlApprove hitlDecision = "approve"
	hitlReject  hitlDecision = "reject"
	hitlDefer   hitlDecision = "defer"
)

func hitlTriggersEnabled() bool {
	return envBoolDefault(hitlTriggersEnabledEnv, true)
}

func hitlInteractiveEnabled() bool {
	return envBoolDefault(hitlInteractiveEnv, true)
}

func hitlTimeout() time.Duration {
	sec := envIntDefault(hitlTimeoutSecEnv, 30)
	sec = clampInt(sec, 5, 180)
	return time.Duration(sec) * time.Second
}

func hitlStreamTriggerEnabled() bool {
	return envBoolDefault(hitlStreamTriggerEnv, true)
}

func canPromptHITL() bool {
	if !hitlTriggersEnabled() || !hitlInteractiveEnabled() {
		return false
	}
	if hitlInputReader == nil || hitlOutput == nil {
		return false
	}
	if hitlIsTTYFn == nil {
		return false
	}
	return hitlIsTTYFn()
}

func promptHITL(triggerID string, summary string, options []hitlDecision) hitlDecision {
	if !canPromptHITL() {
		return hitlDefer
	}
	if len(options) == 0 {
		options = []hitlDecision{hitlApprove, hitlReject, hitlDefer}
	}
	hitlPromptMu.Lock()
	defer hitlPromptMu.Unlock()

	allowed := map[string]hitlDecision{}
	tokens := make([]string, 0, len(options))
	for _, d := range options {
		token := strings.ToLower(strings.TrimSpace(string(d)))
		if token == "" {
			continue
		}
		allowed[token] = d
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return hitlDefer
	}

	_, _ = fmt.Fprintf(hitlOutput, "HITL Trigger [%s]\n%s\nDecision (%s): ", strings.TrimSpace(triggerID), strings.TrimSpace(summary), strings.Join(tokens, "/"))
	resultCh := make(chan hitlDecision, 1)
	scanner := bufio.NewScanner(hitlInputReader)
	go func() {
		if scanner.Scan() {
			raw := strings.ToLower(strings.TrimSpace(scanner.Text()))
			if d, ok := allowed[raw]; ok {
				resultCh <- d
				return
			}
		}
		resultCh <- hitlDefer
	}()
	select {
	case d := <-resultCh:
		_, _ = fmt.Fprintln(hitlOutput)
		return d
	case <-time.After(hitlTimeout()):
		_, _ = fmt.Fprintln(hitlOutput, "\nHITL timeout reached; deferring.")
		return hitlDefer
	}
}

func isTTYInput() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
