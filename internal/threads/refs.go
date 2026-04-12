package threads

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agynio/agyn-cli/internal/config"
)

const RefsFile = "threads.json"

type RefStore struct {
	path string
}

func NewRefStore(path string) RefStore {
	return RefStore{path: path}
}

func DefaultRefStore() (RefStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return RefStore{}, fmt.Errorf("home dir: %w", err)
	}
	return RefStore{path: filepath.Join(home, config.ConfigDir, RefsFile)}, nil
}

func (s RefStore) Load() (map[string]string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read thread refs: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("thread refs file is empty: %s", s.path)
	}
	refs := map[string]string{}
	if err := json.Unmarshal(data, &refs); err != nil {
		return nil, fmt.Errorf("parse thread refs: %w", err)
	}
	return refs, nil
}

func (s RefStore) Save(refs map[string]string) error {
	if refs == nil {
		refs = map[string]string{}
	}
	data, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal thread refs: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create thread refs dir: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("write thread refs: %w", err)
	}
	return nil
}

func ResolveRef(refs map[string]string, value string) (string, bool) {
	threadID, ok := refs[value]
	return threadID, ok
}

func RefForThread(refs map[string]string, threadID string) string {
	for ref, id := range refs {
		if id == threadID {
			return ref
		}
	}
	return ""
}
