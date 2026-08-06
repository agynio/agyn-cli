// Package session holds a sync session's durable state and the cycle that
// drives it.
//
// Everything lives under ~/.agyn/sync/sessions/<id>/. Nothing persistent is
// added to the engineer's directory: the platform does not write there, and the
// identity of the local root is tracked by inode rather than by a marker file.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/agynio/agyn-cli/third_party/mutagen/pkg/synchronization/core"
)

// ancestorFormat versions the reconciliation base on disk. It is never
// migrated: a CLI that does not recognize the stored format discards it and
// re-derives from both sides rather than guessing at a merge base it cannot
// read.
const ancestorFormat = 1

// State is what survives a daemon restart.
type State struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Identity is the (local root, sandbox, remote root) triple. The name is
	// only a label.
	LocalRoot  string `json:"local_root"`
	SandboxID  string `json:"sandbox_id"`
	Sandbox    string `json:"sandbox"`
	RemoteRoot string `json:"remote_root"`

	// LocalRootInode identifies the local directory itself. Recorded at
	// creation and re-checked before every cycle: an unmounted drive, a
	// directory deleted and recreated, and a mountpoint that never mounted each
	// present a different inode, and each would otherwise scan as an empty tree
	// and propagate as "everything was deleted".
	//
	// The device number is deliberately absent. st_dev is assigned at mount
	// time rather than being a property of the filesystem, so it changes across
	// remounts on btrfs subvolumes, device-mapper, overlayfs, external drives,
	// and most macOS non-boot volumes — and sessions are expected to survive
	// reboots.
	LocalRootInode uint64 `json:"local_root_inode"`

	Status     Status    `json:"status"`
	StatusNote string    `json:"status_note,omitempty"`
	LastSync   time.Time `json:"last_sync,omitempty"`

	Conflicts []Conflict `json:"conflicts,omitempty"`
}

type Status string

const (
	StatusIdle    Status = "idle"
	StatusSyncing Status = "syncing"
	// StatusPaused is a sandbox that stopped. Automatic resumption never
	// restarts it — a background file change must not start billable compute.
	StatusPaused Status = "paused"
	// StatusHalted is durable and safe to remain in indefinitely, which is what
	// makes it acceptable for a background process nobody may be watching.
	StatusHalted Status = "halted"
)

// Conflict is a path both sides changed, quarantined rather than resolved.
type Conflict struct {
	Path         string `json:"path"`
	LocalChange  string `json:"local_change"`
	RemoteChange string `json:"remote_change"`
}

// HaltReason names why a session stopped, because the recovery differs by
// cause and the engineer is asserting something different in each case.
type HaltReason string

const (
	HaltRootReplaced   HaltReason = "root-replaced"
	HaltContentLoss    HaltReason = "content-loss"
	HaltSandboxGone    HaltReason = "sandbox-terminated"
	HaltVersionGap     HaltReason = "version-gap"
	HaltAuthentication HaltReason = "authentication-required"
)

// Store is the on-disk home of every session.
type Store struct {
	root string
}

func NewStore(root string) *Store { return &Store{root: root} }

// DefaultRoot is ~/.agyn/sync.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agyn", "sync"), nil
}

func (s *Store) Dir(id string) string      { return filepath.Join(s.root, "sessions", id) }
func (s *Store) TrashDir(id string) string { return filepath.Join(s.Dir(id), "trash") }
func (s *Store) Root() string              { return s.root }

func (s *Store) statePath(id string) string    { return filepath.Join(s.Dir(id), "session.json") }
func (s *Store) ancestorPath(id string) string { return filepath.Join(s.Dir(id), "ancestor.bin") }

// List returns every persisted session, in name order.
func (s *Store) List() ([]*State, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "sessions"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	states := make([]*State, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		state, err := s.Load(entry.Name())
		if err != nil {
			continue
		}
		states = append(states, state)
	}
	return states, nil
}

func (s *Store) Load(id string) (*State, error) {
	data, err := os.ReadFile(s.statePath(id))
	if err != nil {
		return nil, err
	}
	state := &State{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	return state, nil
}

// Save writes through a temporary and renames, so a crash mid-write cannot
// leave a session unreadable.
func (s *Store) Save(state *State) error {
	dir := s.Dir(state.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, s.statePath(state.ID))
}

func (s *Store) Remove(id string) error {
	return os.RemoveAll(s.Dir(id))
}

// ancestorEnvelope carries the format version alongside the tree, so an
// unreadable base is recognized as such rather than misparsed.
type ancestorEnvelope struct {
	Format int    `json:"format"`
	Tree   []byte `json:"tree"`
}

// LoadAncestor returns the reconciliation base. A missing or unrecognized base
// is not an error: it means the next cycle re-derives from both sides in the
// conflict-preserving mode, which is the safe answer.
func (s *Store) LoadAncestor(id string) *core.Entry {
	data, err := os.ReadFile(s.ancestorPath(id))
	if err != nil {
		return nil
	}
	envelope := &ancestorEnvelope{}
	if err := json.Unmarshal(data, envelope); err != nil {
		return nil
	}
	if envelope.Format != ancestorFormat {
		return nil
	}
	entry := &core.Entry{}
	if err := proto.Unmarshal(envelope.Tree, entry); err != nil {
		return nil
	}
	return entry
}

// SaveAncestor records the agreed state. It is called only after a complete
// cycle: an interruption at any earlier point recomputes on the next pass
// rather than recording a state that is not true on disk.
func (s *Store) SaveAncestor(id string, ancestor *core.Entry) error {
	if ancestor == nil {
		return os.Remove(s.ancestorPath(id))
	}
	tree, err := proto.Marshal(ancestor)
	if err != nil {
		return err
	}
	data, err := json.Marshal(&ancestorEnvelope{Format: ancestorFormat, Tree: tree})
	if err != nil {
		return err
	}
	dir := s.Dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".ancestor-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, s.ancestorPath(id))
}

// NameFor labels a session for its local directory and its sandbox. The label
// takes a discriminator on collision; identity is the triple, not the name.
func NameFor(localRoot, sandbox string, taken func(string) bool) string {
	base := fmt.Sprintf("%s-%s", filepath.Base(localRoot), sandbox)
	if !taken(base) {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !taken(candidate) {
			return candidate
		}
	}
}

// Overlaps reports whether two roots nest. Two engines writing one subtree
// cannot be reconciled, so a session that would overlap an existing one is
// refused at creation.
func Overlaps(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+string(filepath.Separator)) ||
		strings.HasPrefix(b, a+string(filepath.Separator))
}
