package gemini

import (
	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

// Adapter implements clients.ClientAdapter for Gemini CLI.
type Adapter struct{}

var _ clients.ClientAdapter = Adapter{}

func (Adapter) Target() string {
	return Target
}

func (Adapter) DefaultConfigPath() (string, error) {
	return DefaultProjectConfigPath()
}

// SupportsRemote is false: remote manifests stay ineligible until this
// adapter materializes remote transports.
func (Adapter) SupportsRemote() bool {
	return false
}

func (Adapter) Sync(manifests []registry.Manifest, opts clients.SyncOptions) (clients.SyncResult, error) {
	return Sync(manifests, opts)
}

func (Adapter) AttachRing(ring registry.Ring, manifests []registry.Manifest, opts clients.SyncOptions) (clients.SyncResult, error) {
	return AttachRing(ring, manifests, opts)
}

func (Adapter) DetachRing(ring string, opts clients.SyncOptions) (clients.SyncResult, error) {
	return DetachRing(ring, opts)
}
