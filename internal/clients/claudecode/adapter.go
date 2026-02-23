package claudecode

import (
	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

// Adapter implements clients.ClientAdapter for Claude Code.
type Adapter struct{}

var _ clients.ClientAdapter = Adapter{}

func (Adapter) Target() string {
	return Target
}

func (Adapter) DefaultConfigPath() (string, error) {
	return DefaultProjectConfigPath()
}

func (Adapter) Sync(manifests []registry.Manifest, opts clients.SyncOptions) (clients.SyncResult, error) {
	return Sync(manifests, opts)
}
