package clients

import (
	"errors"

	"github.com/ankitvg/madari/internal/registry"
)

// ErrConflict indicates a managed/unmanaged name collision during sync.
var ErrConflict = errors.New("sync conflict with unmanaged server")

// ClientAdapter synchronizes Madari manifests into a client's MCP config shape.
type ClientAdapter interface {
	// Target is the stable sync target id, used by `madari sync <client>`.
	Target() string
	// DefaultConfigPath resolves the adapter's default config path.
	DefaultConfigPath() (string, error)
	// Sync computes/applies config changes for the target client.
	Sync(manifests []registry.Manifest, opts SyncOptions) (SyncResult, error)
}

// SyncOptions controls adapter sync behavior.
type SyncOptions struct {
	ConfigPath string
	StatePath  string
	DryRun     bool
}

// SyncResult captures planned/applied mutations from a sync run.
type SyncResult struct {
	ConfigPath string
	DryRun     bool
	Added      []string
	Updated    []string
	Removed    []string
	Unchanged  []string
}

// HasChanges reports whether sync produces any mutation.
func (r SyncResult) HasChanges() bool {
	return len(r.Added)+len(r.Updated)+len(r.Removed) > 0
}
