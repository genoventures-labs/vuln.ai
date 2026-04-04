package rag

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

// IndexOptions configures bulk document indexing behavior.
type IndexOptions struct {
	Recursive               bool
	MaxChunkChars           int
	ChunkOverlap            int
	Incremental             bool
	IncrementalManifestPath string
	GenerateChunkTitles     bool
	ChunkTitleMaxChars      int
	ChunkTitleModel         string
	Extensions              map[string]bool
	AllTypes                bool
	OnFileEvent             func(FileEvent)
	OnSourceIndexed         func(SourceIndexedEvent)
}

// IndexStats summarizes an indexing run.
type IndexStats struct {
	FilesScanned       int
	FilesIndexed       int
	ChunksIndexed      int
	FilesUnchanged     int
	FilesChanged       int
	FilesAdded         int
	FilesRemoved       int
	ChunksArchived     int
	SkippedUnsupported int
	SkippedBinary      int
	ParseErrors        int
	IndexErrors        int
	WalkErrors         int
}

// FileEvent reports per-file indexing progress and outcomes.
type FileEvent struct {
	Path         string
	RelPath      string
	Outcome      string
	Chunks       int
	Error        string
	FilesScanned int
	FilesIndexed int
}

// SourceIndexedEvent is emitted when one logical source document has been indexed.
type SourceIndexedEvent struct {
	SourceType string
	SourceRef  string
	Content    string
	ChunkCount int
	Metadata   map[string]string
}

// DefaultIndexOptions returns pragmatic defaults for document indexing.
func DefaultIndexOptions() IndexOptions {
	return IndexOptions{
		Recursive:           true,
		MaxChunkChars:       1200,
		ChunkOverlap:        200,
		Incremental:         true,
		GenerateChunkTitles: true,
		ChunkTitleMaxChars:  96,
		ChunkTitleModel:     "",
		Extensions: map[string]bool{
			".pdf":      true,
			".md":       true,
			".markdown": true,
			".txt":      true,
			".rst":      true,
			".adoc":     true,
			".json":     true,
			".yaml":     true,
			".yml":      true,
			".csv":      true,
			".tsv":      true,
			".html":     true,
			".htm":      true,
		},
	}
}

// ParseExtensionsCSV converts ".md,.pdf" style input to extension map.
func ParseExtensionsCSV(input string) map[string]bool {
	exts := make(map[string]bool)
	for _, part := range strings.Split(input, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		if !strings.HasPrefix(part, ".") {
			part = "." + part
		}
		exts[part] = true
	}
	return exts
}

// ExtensionsToCSV serializes extension map into deterministic CSV.
func ExtensionsToCSV(exts map[string]bool) string {
	if len(exts) == 0 {
		return ""
	}
	order := []string{
		".pdf", ".md", ".markdown", ".txt", ".rst", ".adoc",
		".json", ".yaml", ".yml", ".csv", ".tsv", ".html", ".htm",
	}
	var out []string
	seen := make(map[string]bool)
	for _, ext := range order {
		if exts[ext] {
			out = append(out, ext)
			seen[ext] = true
		}
	}
	for ext := range exts {
		if !seen[ext] {
			out = append(out, ext)
		}
	}
	return strings.Join(out, ",")
}

// IndexDirectory indexes supported files under a directory into knowledge memory.
func IndexDirectory(mm *memory.MemoryManager, dir string, opts IndexOptions) (IndexStats, error) {
	if opts.MaxChunkChars <= 0 {
		opts.MaxChunkChars = 1200
	}
	if opts.ChunkOverlap < 0 {
		opts.ChunkOverlap = 0
	}
	if opts.ChunkOverlap >= opts.MaxChunkChars {
		opts.ChunkOverlap = opts.MaxChunkChars / 4
	}
	if !opts.GenerateChunkTitles && opts.ChunkTitleMaxChars <= 0 && strings.TrimSpace(opts.ChunkTitleModel) == "" {
		opts.GenerateChunkTitles = true
	}
	if opts.ChunkTitleMaxChars <= 0 {
		opts.ChunkTitleMaxChars = 96
	}
	if len(opts.Extensions) == 0 {
		opts.Extensions = DefaultIndexOptions().Extensions
	}
	if opts.AllTypes {
		opts.Extensions = nil
	}
	var stats IndexStats
	absRoot, err := filepath.Abs(dir)
	if err != nil {
		return stats, fmt.Errorf("failed to resolve directory path: %w", err)
	}
	manifestPath := strings.TrimSpace(opts.IncrementalManifestPath)
	if manifestPath == "" {
		manifestPath = ManifestPathForRoot(absRoot)
	}
	manifest := IncrementalManifest{
		Version: 1,
		Root:    absRoot,
		Files:   map[string]IncrementalFileRecord{},
	}
	if opts.Incremental {
		loaded, loadErr := LoadIncrementalManifest(manifestPath)
		if loadErr != nil {
			return stats, fmt.Errorf("failed loading incremental manifest: %w", loadErr)
		}
		if loaded.Version <= 0 {
			loaded.Version = 1
		}
		if loaded.Files == nil {
			loaded.Files = map[string]IncrementalFileRecord{}
		}
		if strings.TrimSpace(loaded.Root) == "" {
			loaded.Root = absRoot
		}
		manifest = loaded
	}

	titleGen := memory.NewChunkTitleGenerator(mm, memory.ChunkTitleConfig{
		MaxChars: opts.ChunkTitleMaxChars,
		Model:    strings.TrimSpace(opts.ChunkTitleModel),
	})
	type candidateFile struct {
		AbsPath  string
		RelPath  string
		Ext      string
		Size     int64
		ModTime  int64
		FastHash string
	}
	candidates := make([]candidateFile, 0, 128)
	currentSeen := map[string]bool{}
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			stats.WalkErrors++
			emitFileEvent(opts, FileEvent{
				Path:         path,
				RelPath:      relPath(absRoot, path),
				Outcome:      "walk-error",
				Error:        walkErr.Error(),
				FilesScanned: stats.FilesScanned,
				FilesIndexed: stats.FilesIndexed,
			})
			return nil
		}
		if d.IsDir() {
			if !opts.Recursive && path != absRoot {
				return filepath.SkipDir
			}
			return nil
		}

		stats.FilesScanned++
		ext := strings.ToLower(filepath.Ext(path))
		if !opts.AllTypes && !opts.Extensions[ext] {
			stats.SkippedUnsupported++
			emitFileEvent(opts, FileEvent{
				Path:         path,
				RelPath:      relPath(absRoot, path),
				Outcome:      "skipped-unsupported",
				FilesScanned: stats.FilesScanned,
				FilesIndexed: stats.FilesIndexed,
			})
			return nil
		}
		rp := relPath(absRoot, path)
		size, mod, fastHash, fpErr := ComputeFastFileFingerprint(path, 256*1024)
		if fpErr != nil {
			stats.ParseErrors++
			emitFileEvent(opts, FileEvent{
				Path:         path,
				RelPath:      rp,
				Outcome:      "fingerprint-error",
				Error:        fpErr.Error(),
				FilesScanned: stats.FilesScanned,
				FilesIndexed: stats.FilesIndexed,
			})
			return nil
		}
		currentSeen[rp] = true
		candidates = append(candidates, candidateFile{
			AbsPath:  path,
			RelPath:  rp,
			Ext:      ext,
			Size:     size,
			ModTime:  mod,
			FastHash: fastHash,
		})
		return nil
	})
	if err != nil {
		return stats, err
	}

	for _, c := range candidates {
		if opts.Incremental {
			prev, ok := manifest.Files[c.RelPath]
			if ok && prev.Size == c.Size && prev.ModTimeUnix == c.ModTime && prev.FastHash == c.FastHash {
				stats.FilesUnchanged++
				emitFileEvent(opts, FileEvent{
					Path:         c.AbsPath,
					RelPath:      c.RelPath,
					Outcome:      "unchanged",
					FilesScanned: stats.FilesScanned,
					FilesIndexed: stats.FilesIndexed,
				})
				continue
			}
			if ok {
				stats.FilesChanged++
				archived, _ := mm.ArchiveKnowledgeBySourcePath(c.RelPath, "incremental_replaced_source")
				stats.ChunksArchived += archived
			} else {
				stats.FilesAdded++
			}
		}

		content, parseErr := readDocument(c.AbsPath, c.Ext)
		if parseErr != nil {
			if strings.Contains(parseErr.Error(), "binary file") {
				stats.SkippedBinary++
				emitFileEvent(opts, FileEvent{
					Path:         c.AbsPath,
					RelPath:      c.RelPath,
					Outcome:      "skipped-binary",
					Error:        parseErr.Error(),
					FilesScanned: stats.FilesScanned,
					FilesIndexed: stats.FilesIndexed,
				})
			} else {
				stats.ParseErrors++
				emitFileEvent(opts, FileEvent{
					Path:         c.AbsPath,
					RelPath:      c.RelPath,
					Outcome:      "parse-error",
					Error:        parseErr.Error(),
					FilesScanned: stats.FilesScanned,
					FilesIndexed: stats.FilesIndexed,
				})
			}
			continue
		}
		content = normalizeText(content)
		if strings.TrimSpace(content) == "" {
			emitFileEvent(opts, FileEvent{
				Path:         c.AbsPath,
				RelPath:      c.RelPath,
				Outcome:      "empty",
				FilesScanned: stats.FilesScanned,
				FilesIndexed: stats.FilesIndexed,
			})
			continue
		}

		chunks := chunkText(content, opts.MaxChunkChars, opts.ChunkOverlap)
		if len(chunks) == 0 {
			emitFileEvent(opts, FileEvent{
				Path:         c.AbsPath,
				RelPath:      c.RelPath,
				Outcome:      "empty",
				FilesScanned: stats.FilesScanned,
				FilesIndexed: stats.FilesIndexed,
			})
			continue
		}
		sectionMetas := inferChunkSections(chunks, c.RelPath)

		indexFailed := false
		for i, chunk := range chunks {
			metadata := map[string]string{
				"type":          "knowledge",
				"source_type":   "file",
				"source_path":   c.RelPath,
				"topology_node": c.RelPath,
				"source_ext":    c.Ext,
				"chunk_index":   strconv.Itoa(i + 1),
				"chunk_total":   strconv.Itoa(len(chunks)),
				"indexed_at":    time.Now().UTC().Format(time.RFC3339),
			}
			if i < len(sectionMetas) {
				sectionID, sectionTitle, sectionLevel, sectionInferred := sectionMetaAsStrings(sectionMetas[i])
				metadata["section_id"] = sectionID
				metadata["section_title"] = sectionTitle
				metadata["section_level"] = sectionLevel
				metadata["section_inferred"] = sectionInferred
			}
			if opts.GenerateChunkTitles {
				title, titleErr := titleGen.Generate(context.Background(), "file", c.RelPath, chunk, i+1, len(chunks))
				if titleErr != nil {
					title = deterministicChunkFallbackTitle(c.RelPath, i+1, len(chunks))
				}
				metadata["chunk_title"] = title
			}
			if err := mm.AddKnowledge(chunk, metadata); err != nil {
				stats.IndexErrors++
				indexFailed = true
				emitFileEvent(opts, FileEvent{
					Path:         c.AbsPath,
					RelPath:      c.RelPath,
					Outcome:      "index-error",
					Error:        fmt.Sprintf("failed to index chunk %d: %v", i+1, err),
					FilesScanned: stats.FilesScanned,
					FilesIndexed: stats.FilesIndexed,
				})
				break
			}
		}
		if indexFailed {
			continue
		}

		stats.FilesIndexed++
		stats.ChunksIndexed += len(chunks)
		if topoErr := mm.UpsertTopologySource(memory.SourceFingerprint{
			SourceType: "file",
			SourceRef:  c.RelPath,
			SourcePath: c.RelPath,
			Content:    content,
		}); topoErr != nil {
			emitFileEvent(opts, FileEvent{
				Path:         c.AbsPath,
				RelPath:      c.RelPath,
				Outcome:      "topology-error",
				Error:        topoErr.Error(),
				FilesScanned: stats.FilesScanned,
				FilesIndexed: stats.FilesIndexed,
			})
		}
		emitFileEvent(opts, FileEvent{
			Path:         c.AbsPath,
			RelPath:      c.RelPath,
			Outcome:      "indexed",
			Chunks:       len(chunks),
			FilesScanned: stats.FilesScanned,
			FilesIndexed: stats.FilesIndexed,
		})
		emitSourceIndexed(opts, SourceIndexedEvent{
			SourceType: "file",
			SourceRef:  c.RelPath,
			Content:    content,
			ChunkCount: len(chunks),
			Metadata: map[string]string{
				"source_type": "file",
				"source_path": c.RelPath,
				"source_ext":  c.Ext,
			},
		})
		if opts.Incremental {
			manifest.Files[c.RelPath] = IncrementalFileRecord{
				RelPath:       c.RelPath,
				Size:          c.Size,
				ModTimeUnix:   c.ModTime,
				FastHash:      c.FastHash,
				LastIndexedAt: time.Now().UTC().Format(time.RFC3339),
			}
		}
	}
	if opts.Incremental {
		for rel := range manifest.Files {
			if currentSeen[rel] {
				continue
			}
			stats.FilesRemoved++
			archived, _ := mm.ArchiveKnowledgeBySourcePath(rel, "incremental_removed_source")
			stats.ChunksArchived += archived
			delete(manifest.Files, rel)
		}
		manifest.Root = absRoot
		if err := SaveIncrementalManifest(manifestPath, manifest); err != nil {
			return stats, fmt.Errorf("failed writing incremental manifest: %w", err)
		}
	}

	return stats, nil
}

func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}

func emitFileEvent(opts IndexOptions, event FileEvent) {
	if opts.OnFileEvent != nil {
		opts.OnFileEvent(event)
	}
}

func emitSourceIndexed(opts IndexOptions, event SourceIndexedEvent) {
	if opts.OnSourceIndexed != nil {
		opts.OnSourceIndexed(event)
	}
}

func readDocument(path, ext string) (string, error) {
	if ext == ".pdf" {
		return readPDF(path)
	}

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	if isLikelyBinary(data) {
		return "", fmt.Errorf("binary file")
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("non-utf8 text file")
	}
	return string(data), nil
}

func readPDF(path string) (string, error) {
	_, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdf indexing requires 'pdftotext' to be installed")
	}
	cmd := exec.Command("pdftotext", "-layout", "-enc", "UTF-8", path, "-")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext failed: %v: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

func isLikelyBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if bytes.Contains(sample, []byte{0}) {
		return true
	}
	return false
}

func normalizeText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func chunkText(content string, maxChars, overlap int) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	runes := []rune(content)
	if len(runes) <= maxChars {
		return []string{content}
	}

	step := maxChars - overlap
	if step <= 0 {
		step = maxChars
	}

	var chunks []string
	for start := 0; start < len(runes); start += step {
		end := start + maxChars
		if end > len(runes) {
			end = len(runes)
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(runes) {
			break
		}
	}
	return chunks
}

func deterministicChunkFallbackTitle(sourceRef string, index int, total int) string {
	base := strings.TrimSpace(filepath.Base(strings.TrimSpace(sourceRef)))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "chunk"
	}
	if total > 1 && index > 0 {
		return fmt.Sprintf("%s [%d/%d]", base, index, total)
	}
	return base
}
