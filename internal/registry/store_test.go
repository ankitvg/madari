package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(filepath.Join(tmp, "servers"))

	manifest := baseManifest()

	if err := store.Add(manifest); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	if err := store.Add(manifest); err == nil {
		t.Fatalf("expected add duplicate to fail")
	}

	loaded, err := store.Get(manifest.Name)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if loaded.Name != manifest.Name || loaded.Command != manifest.Command {
		t.Fatalf("unexpected loaded manifest: %#v", loaded)
	}

	if err := store.SetEnabled(manifest.Name, false); err != nil {
		t.Fatalf("set enabled failed: %v", err)
	}

	loaded, err = store.Get(manifest.Name)
	if err != nil {
		t.Fatalf("get after set enabled failed: %v", err)
	}
	if loaded.Enabled {
		t.Fatalf("expected enabled=false after toggle")
	}

	manifests, err := store.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(manifests) != 1 || manifests[0].Name != manifest.Name {
		t.Fatalf("unexpected list result: %#v", manifests)
	}

	if err := store.Remove(manifest.Name); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	_, err = store.Get(manifest.Name)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after remove, got: %v", err)
	}
}

func TestDefaultRootDirUsesConfigDirOverride(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	t.Setenv(ConfigDirEnvVar, "~/custom-madari")

	root, err := DefaultRootDir()
	if err != nil {
		t.Fatalf("default root with override: %v", err)
	}

	expected := filepath.Join(home, "custom-madari")
	if root != expected {
		t.Fatalf("expected override root %q, got %q", expected, root)
	}
}

func TestDefaultRootDirUsesUserConfigDir(t *testing.T) {
	t.Setenv(ConfigDirEnvVar, "")

	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("resolve user config dir: %v", err)
	}

	root, err := DefaultRootDir()
	if err != nil {
		t.Fatalf("default root: %v", err)
	}

	expected := filepath.Join(configDir, "madari")
	if root != expected {
		t.Fatalf("expected default root %q, got %q", expected, root)
	}
}

func TestDefaultRootDirOverrideTrimsAndCleansPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	t.Setenv(ConfigDirEnvVar, "  ~/madari-space/../madari-space  ")

	root, err := DefaultRootDir()
	if err != nil {
		t.Fatalf("default root with spaced override: %v", err)
	}

	expected := filepath.Join(home, "madari-space")
	if root != expected {
		t.Fatalf("expected cleaned override root %q, got %q", expected, root)
	}
}

func TestDefaultServersDirUsesOverrideRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	t.Setenv(ConfigDirEnvVar, "~/custom-madari")

	serversDir, err := DefaultServersDir()
	if err != nil {
		t.Fatalf("default servers dir with override: %v", err)
	}

	expected := filepath.Join(home, "custom-madari", "servers")
	if serversDir != expected {
		t.Fatalf("expected override servers dir %q, got %q", expected, serversDir)
	}
}

func TestDefaultServersDirUsesUserConfigRoot(t *testing.T) {
	t.Setenv(ConfigDirEnvVar, "")

	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("resolve user config dir: %v", err)
	}

	serversDir, err := DefaultServersDir()
	if err != nil {
		t.Fatalf("default servers dir: %v", err)
	}

	expected := filepath.Join(configDir, "madari", "servers")
	if serversDir != expected {
		t.Fatalf("expected default servers dir %q, got %q", expected, serversDir)
	}
}
