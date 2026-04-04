package envload

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const envFileOverride = "PLM_ENV_FILE"
const defaultOllamaHost = "http://85.31.233.157:11434"

// Autoload loads .env variables into process env without overriding explicit shell vars.
// Resolution order:
// 1) PLM_ENV_FILE (if set)
// 2) nearest directory from cwd upward containing .env or .env.local
func Autoload() error {
	override := strings.TrimSpace(os.Getenv(envFileOverride))
	if override != "" {
		return loadFromPath(override)
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	targetDir, ok := findNearestEnvDir(dir)
	if !ok {
		return ensureRuntimeDefaults()
	}

	for _, name := range []string{".env", ".env.local"} {
		p := filepath.Join(targetDir, name)
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := loadFromPath(p); err != nil {
			return err
		}
	}
	return ensureRuntimeDefaults()
}

func ensureRuntimeDefaults() error {
	if strings.TrimSpace(os.Getenv("OLLAMA_HOST")) == "" {
		if err := os.Setenv("OLLAMA_HOST", defaultOllamaHost); err != nil {
			return fmt.Errorf("set OLLAMA_HOST default: %w", err)
		}
	}
	return nil
}

func findNearestEnvDir(start string) (string, bool) {
	cur := filepath.Clean(start)
	for {
		if fileExists(filepath.Join(cur, ".env")) || fileExists(filepath.Join(cur, ".env.local")) {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func loadFromPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)
		if err := os.Setenv(key, val); err != nil {
			return fmt.Errorf("set %s from %s: %w", key, path, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}
