package taloscli

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type learnProgress struct {
	enabled      bool
	startedAt    time.Time
	phase        string
	phaseIndex   int
	phaseTotal   int
	scanned      int
	indexed      int
	chunks       int
	skipped      int
	errors       int
	itemsFetched int
	lastLen      int
}

func newLearnProgress() *learnProgress {
	p := &learnProgress{startedAt: time.Now(), enabled: true}
	info, err := os.Stdout.Stat()
	if err != nil || (info.Mode()&os.ModeCharDevice) == 0 {
		p.enabled = false
	}
	return p
}

func (p *learnProgress) setPhase(name string, idx int, total int) {
	p.phase = strings.TrimSpace(name)
	p.phaseIndex = idx
	p.phaseTotal = total
	p.render()
}

func (p *learnProgress) onFileEvent(outcome string, chunks int, filesScanned int, filesIndexed int) {
	if filesScanned > p.scanned {
		p.scanned = filesScanned
	}
	if filesIndexed > p.indexed {
		p.indexed = filesIndexed
	}
	switch strings.TrimSpace(outcome) {
	case "indexed":
		if chunks > 0 {
			p.chunks += chunks
		}
	case "skipped-unsupported", "skipped-binary":
		p.skipped++
	case "parse-error", "index-error", "walk-error":
		p.errors++
	}
	p.render()
}

func (p *learnProgress) onSourceIndexed(chunks int) {
	p.indexed++
	p.chunks += chunks
	p.render()
}

func (p *learnProgress) onRemoteEvent(outcome string) {
	p.itemsFetched++
	switch strings.TrimSpace(strings.ToLower(outcome)) {
	case "indexed":
		p.indexed++
	case "error", "blocked":
		p.errors++
	default:
		p.skipped++
	}
	p.render()
}

func (p *learnProgress) addError() {
	p.errors++
	p.render()
}

func (p *learnProgress) verbosef(format string, args ...any) {
	if !learnVerbose {
		return
	}
	p.clearLine()
	fmt.Printf(format, args...)
	if !strings.HasSuffix(format, "\n") {
		fmt.Println()
	}
	p.render()
}

func (p *learnProgress) finish() {
	if !p.enabled {
		return
	}
	fmt.Print("\n")
	p.lastLen = 0
}

func (p *learnProgress) clearLine() {
	if !p.enabled || p.lastLen <= 0 {
		return
	}
	fmt.Printf("\r%s\r", strings.Repeat(" ", p.lastLen))
}

func (p *learnProgress) render() {
	elapsed := time.Since(p.startedAt).Round(time.Second)
	phase := p.phase
	if phase == "" {
		phase = "learn"
	}
	prefix := phase
	if p.phaseTotal > 0 && p.phaseIndex > 0 {
		prefix = fmt.Sprintf("%s %d/%d", phase, p.phaseIndex, p.phaseTotal)
	}
	line := fmt.Sprintf("[progress] %s | scanned:%d fetched:%d indexed:%d chunks:%d skipped:%d errors:%d elapsed:%s",
		prefix, p.scanned, p.itemsFetched, p.indexed, p.chunks, p.skipped, p.errors, elapsed)
	if p.enabled {
		out := "\r" + line
		if p.lastLen > len(line) {
			out += strings.Repeat(" ", p.lastLen-len(line))
		}
		fmt.Print(out)
		p.lastLen = len(line)
		return
	}
	fmt.Println(line)
}
