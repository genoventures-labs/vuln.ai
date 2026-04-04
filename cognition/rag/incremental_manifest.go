package rag

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultFingerprintMaxBytes = int64(256 * 1024)
)

type IncrementalFileRecord struct {
	RelPath       string   `json:"rel_path"`
	Size          int64    `json:"size"`
	ModTimeUnix   int64    `json:"mod_time_unix"`
	FastHash      string   `json:"fast_hash"`
	ChunkIDs      []string `json:"chunk_ids,omitempty"`
	LastIndexedAt string   `json:"last_indexed_at"`
}

type IncrementalManifest struct {
	Version int                              `json:"version"`
	Root    string                           `json:"root"`
	Files   map[string]IncrementalFileRecord `json:"files"`
}

func ManifestPathForRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}
	sum := sha1.Sum([]byte(strings.ToLower(root)))
	return filepath.Join(".memory", "incremental", hex.EncodeToString(sum[:8])+".json")
}

func LoadIncrementalManifest(path string) (IncrementalManifest, error) {
	out := IncrementalManifest{
		Version: 1,
		Files:   map[string]IncrementalFileRecord{},
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	if strings.TrimSpace(string(b)) == "" {
		return out, nil
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	if out.Version <= 0 {
		out.Version = 1
	}
	if out.Files == nil {
		out.Files = map[string]IncrementalFileRecord{}
	}
	return out, nil
}

func SaveIncrementalManifest(path string, m IncrementalManifest) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if m.Version <= 0 {
		m.Version = 1
	}
	if m.Files == nil {
		m.Files = map[string]IncrementalFileRecord{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ComputeFastFileFingerprint(path string, maxBytes int64) (int64, int64, string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, "", err
	}
	size := fi.Size()
	mod := fi.ModTime().UTC().Unix()
	if maxBytes <= 0 {
		maxBytes = defaultFingerprintMaxBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, "", err
	}
	defer f.Close()

	h := sha1.New()
	_, _ = io.WriteString(h, "size="+strconv.FormatInt(size, 10)+"|mod="+strconv.FormatInt(mod, 10)+"|")
	if size <= maxBytes {
		if _, err := io.Copy(h, f); err != nil {
			return 0, 0, "", err
		}
		return size, mod, hex.EncodeToString(h.Sum(nil)), nil
	}

	part := maxBytes / 3
	if part < 1024 {
		part = 1024
	}
	readChunk := func(off int64) error {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return err
		}
		_, err := io.CopyN(h, f, part)
		if err != nil && err != io.EOF {
			return err
		}
		return nil
	}
	if err := readChunk(0); err != nil {
		return 0, 0, "", err
	}
	mid := size/2 - part/2
	if mid < 0 {
		mid = 0
	}
	if err := readChunk(mid); err != nil {
		return 0, 0, "", err
	}
	tail := size - part
	if tail < 0 {
		tail = 0
	}
	if err := readChunk(tail); err != nil {
		return 0, 0, "", err
	}
	return size, mod, hex.EncodeToString(h.Sum(nil)), nil
}
