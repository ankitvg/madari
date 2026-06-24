//go:build windows

package syncshared

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWriteFileAtomicallyPrivateModeUsesOwnerOnlyACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")

	if err := WriteFileAtomically(path, []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatalf("write private file: %v", err)
	}

	assertOwnerOnlyDACL(t, path)
}

func TestBackupFileWithModePrivateModeUsesOwnerOnlyACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(path, []byte(`{"token":"secret"}`), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	backupPath, err := BackupFileWithMode(path, 0o600)
	if err != nil {
		t.Fatalf("backup private file: %v", err)
	}

	assertOwnerOnlyDACL(t, backupPath)
}

func assertOwnerOnlyDACL(t *testing.T, path string) {
	t.Helper()

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("get current user token: %v", err)
	}
	wantSID := user.User.Sid

	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("get security descriptor: %v", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("get DACL: %v", err)
	}
	if dacl == nil {
		t.Fatalf("expected DACL on %s", path)
	}
	if dacl.AceCount != 1 {
		t.Fatalf("expected one ACL entry on %s, got %d", path, dacl.AceCount)
	}

	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("get ACL entry: %v", err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Fatalf("expected allow ACE, got type %d", ace.Header.AceType)
	}
	if ace.Mask != windows.GENERIC_ALL {
		t.Fatalf("expected GENERIC_ALL mask, got %#x", ace.Mask)
	}
	gotSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !wantSID.Equals(gotSID) {
		t.Fatalf("expected ACL SID %s, got %s", wantSID.String(), gotSID.String())
	}
}
