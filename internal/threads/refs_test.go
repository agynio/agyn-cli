package threads

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRefStoreSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.json")
	store := NewRefStore(path)
	refs := map[string]string{
		"research": "550e8400-e29b-41d4-a716-446655440000",
		"plan":     "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
	}
	if err := store.Save(refs); err != nil {
		t.Fatalf("save refs: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load refs: %v", err)
	}
	if !reflect.DeepEqual(loaded, refs) {
		t.Fatalf("refs mismatch: got %#v want %#v", loaded, refs)
	}
}

func TestRefLookup(t *testing.T) {
	refs := map[string]string{
		"alpha": "thread-a",
		"beta":  "thread-b",
	}
	threadID, ok := ResolveRef(refs, "alpha")
	if !ok || threadID != "thread-a" {
		t.Fatalf("expected alpha to resolve to thread-a")
	}
}
