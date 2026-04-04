package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type localKeyRecord struct {
	ClientID  string
	APIKey    string
	UpdatedAt time.Time
}

var localEntryRE = regexp.MustCompile(`(?ms)-\s*client_id:\s*"?([^"\n]+)"?\s*\n\s*api_key:\s*"?([^"\n]+)"?`)

// UpsertLocalAPIKey stores/updates a keypair in local api_keys.yaml.
func UpsertLocalAPIKey(path, clientID, apiKey string) error {
	clientID = strings.TrimSpace(clientID)
	apiKey = strings.TrimSpace(apiKey)
	if clientID == "" || apiKey == "" {
		return fmt.Errorf("client_id and api_key are required")
	}
	records, _ := readLocalAPIKeyRecords(path)
	records[clientID] = localKeyRecord{
		ClientID:  clientID,
		APIKey:    apiKey,
		UpdatedAt: time.Now().UTC(),
	}
	return writeLocalAPIKeyRecords(path, records)
}

// RemoveLocalAPIKey removes a client from local api_keys.yaml.
func RemoveLocalAPIKey(path, clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return fmt.Errorf("client_id is required")
	}
	records, _ := readLocalAPIKeyRecords(path)
	delete(records, clientID)
	return writeLocalAPIKeyRecords(path, records)
}

func readLocalAPIKeyRecords(path string) (map[string]localKeyRecord, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]localKeyRecord{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]localKeyRecord{}, nil
		}
		return nil, err
	}
	out := map[string]localKeyRecord{}
	for _, m := range localEntryRE.FindAllStringSubmatch(string(raw), -1) {
		if len(m) < 3 {
			continue
		}
		id := strings.TrimSpace(m[1])
		key := strings.TrimSpace(m[2])
		if id == "" || key == "" {
			continue
		}
		out[id] = localKeyRecord{
			ClientID:  id,
			APIKey:    key,
			UpdatedAt: time.Now().UTC(),
		}
	}
	return out, nil
}

func writeLocalAPIKeyRecords(path string, records map[string]localKeyRecord) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}

	var ids []string
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	b.WriteString("# Local mirrored glm-toolserver credentials. Do not commit this file.\n")
	b.WriteString("clients:\n")
	for _, id := range ids {
		r := records[id]
		if strings.TrimSpace(r.ClientID) == "" || strings.TrimSpace(r.APIKey) == "" {
			continue
		}
		ts := r.UpdatedAt
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		b.WriteString("  - client_id: \"" + yamlEsc(r.ClientID) + "\"\n")
		b.WriteString("    api_key: \"" + yamlEsc(r.APIKey) + "\"\n")
		b.WriteString("    updated_at: \"" + ts.Format(time.RFC3339) + "\"\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func yamlEsc(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), `"`, `\"`)
}
