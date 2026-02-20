package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// ConfigDirEnvVar overrides the default config root directory.
	ConfigDirEnvVar = "MADARI_CONFIG_DIR"
)

var ErrNotFound = errors.New("server not found")

// Store persists server manifests as individual TOML files.
type Store struct {
	serversDir string
}

// NewStore creates a store bound to a servers directory.
func NewStore(serversDir string) *Store {
	return &Store{serversDir: filepath.Clean(serversDir)}
}

// NewDefaultStore creates a store rooted at ~/.config/madari/servers.
func NewDefaultStore() (*Store, error) {
	serversDir, err := DefaultServersDir()
	if err != nil {
		return nil, err
	}
	return NewStore(serversDir), nil
}

// DefaultRootDir resolves the base Madari config directory.
func DefaultRootDir() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(ConfigDirEnvVar)); configured != "" {
		resolved, err := expandHome(configured)
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".config", "madari"), nil
}

// DefaultServersDir resolves the default manifest directory.
func DefaultServersDir() (string, error) {
	root, err := DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "servers"), nil
}

// ServersDir returns the on-disk servers directory.
func (s *Store) ServersDir() string {
	return s.serversDir
}

// Ensure creates the registry directory if needed.
func (s *Store) Ensure() error {
	if s.serversDir == "" {
		return fmt.Errorf("servers directory is empty")
	}
	if err := os.MkdirAll(s.serversDir, 0o755); err != nil {
		return fmt.Errorf("ensure servers directory: %w", err)
	}
	return nil
}

// Add inserts a new server manifest and fails if it already exists.
func (s *Store) Add(m Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if _, err := s.Get(m.Name); err == nil {
		return fmt.Errorf("server %q already exists", m.Name)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return s.Save(m)
}

// Save writes or updates a server manifest.
func (s *Store) Save(m Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if err := s.Ensure(); err != nil {
		return err
	}

	path, err := s.pathForName(m.Name)
	if err != nil {
		return err
	}

	payload, err := MarshalManifest(m)
	if err != nil {
		return err
	}

	if err := writeFileAtomically(path, payload, 0o644); err != nil {
		return fmt.Errorf("save manifest %q: %w", m.Name, err)
	}
	return nil
}

// Get loads one manifest by name.
func (s *Store) Get(name string) (Manifest, error) {
	path, err := s.pathForName(name)
	if err != nil {
		return Manifest{}, err
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, ErrNotFound
		}
		return Manifest{}, fmt.Errorf("read manifest %q: %w", name, err)
	}

	manifest, err := ParseManifest(payload)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %q: %w", name, err)
	}
	if manifest.Name != name {
		return Manifest{}, fmt.Errorf("manifest %q has mismatched name %q", name, manifest.Name)
	}
	return manifest, nil
}

// List returns all manifests sorted by name.
func (s *Store) List() ([]Manifest, error) {
	entries, err := os.ReadDir(s.serversDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Manifest{}, nil
		}
		return nil, fmt.Errorf("read servers directory: %w", err)
	}

	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".toml")
		manifest, err := s.Get(name)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].Name < manifests[j].Name
	})
	return manifests, nil
}

// Remove deletes one manifest by name.
func (s *Store) Remove(name string) error {
	path, err := s.pathForName(name)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("remove manifest %q: %w", name, err)
	}
	return nil
}

// SetEnabled toggles enabled state for one manifest.
func (s *Store) SetEnabled(name string, enabled bool) error {
	manifest, err := s.Get(name)
	if err != nil {
		return err
	}
	manifest.Enabled = enabled
	return s.Save(manifest)
}

func (s *Store) pathForName(name string) (string, error) {
	if err := validateServerName(name); err != nil {
		return "", err
	}
	if strings.TrimSpace(s.serversDir) == "" {
		return "", fmt.Errorf("servers directory is empty")
	}
	return filepath.Join(s.serversDir, name+".toml"), nil
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".madari-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	defer cleanup()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

func expandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
