package codex

import (
	"github.com/ankitvg/madari/internal/clients"
	"github.com/ankitvg/madari/internal/registry"
)

// Adapter implements clients.ClientAdapter for Codex.
type Adapter struct{}

var _ clients.ClientAdapter = Adapter{}

func (Adapter) Target() string {
	return Target
}

func (Adapter) DefaultConfigPath() (string, error) {
	return DefaultConfigPath()
}

// SupportsRemote is true for Streamable HTTP only: Codex materializes http
// manifests as native url entries in config.toml. SSE endpoints are not part
// of Codex's documented remote support and stay pending.
func (Adapter) SupportsRemote(transport string) bool {
	return supportsRemoteTransport(transport)
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
