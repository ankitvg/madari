// Package clients defines the shared adapter contract for syncing Madari
// manifests into target client MCP configs.
//
// Design rationale and tradeoffs are documented in:
// docs/adr/001-client-adapter-interface.md.
package clients

import (
	"errors"

	"github.com/ankitvg/madari/internal/registry"
)

// ErrConflict indicates a managed/unmanaged name collision during sync.
//
// Adapters should return an error wrapping ErrConflict when:
// - a desired server name already exists in client config
// - that entry is not currently managed by Madari
// - and the existing value differs from the desired value
//
// Adapters may treat exact-value matches as unchanged instead of conflicts.
var ErrConflict = errors.New("sync conflict with unmanaged server")

// ClientAdapter synchronizes Madari manifests into a client's MCP config shape.
//
// Safety and behavior invariants:
// - preserve unknown/non-MCP config blocks in the target file.
// - fail closed on config parse errors (do not partially write).
// - dry-run computes changes without mutating config or managed state.
// - apply mode should backup existing config before write and write atomically.
// - managed state must only track entries owned by Madari for the target client.
type ClientAdapter interface {
	// Target is the stable sync target id, used by `madari sync <client>`.
	Target() string
	// DefaultConfigPath resolves the adapter's default config path.
	DefaultConfigPath() (string, error)
	// Sync computes/applies config changes for the target client.
	//
	// On dry-run, return the plan only. On apply, persist config and managed
	// state consistent with the returned plan. Name collisions with unmanaged
	// entries should return an error wrapping ErrConflict.
	Sync(manifests []registry.Manifest, opts SyncOptions) (SyncResult, error)
}

// SyncOptions controls adapter sync behavior.
type SyncOptions struct {
	// ConfigPath overrides adapter default config path when non-empty.
	ConfigPath string
	// StatePath stores names currently managed by Madari for this target.
	StatePath string
	// DryRun computes a plan without writing config or managed state.
	DryRun bool
}

// SyncResult captures planned/applied mutations from a sync run.
type SyncResult struct {
	// ConfigPath is the resolved config path used by the adapter.
	ConfigPath string
	// DryRun mirrors the execution mode used for this result.
	DryRun bool
	// Added are names absent from config and introduced by this sync.
	Added []string
	// Updated are names managed by Madari whose values changed.
	Updated []string
	// Removed are previously managed names no longer desired.
	Removed []string
	// Unchanged are desired names already matching target config values.
	Unchanged []string
}

// HasChanges reports whether sync produces any mutation.
func (r SyncResult) HasChanges() bool {
	return len(r.Added)+len(r.Updated)+len(r.Removed) > 0
}
