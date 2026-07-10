//go:build windows

package executionreceipt

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWriteUsesOwnerOnlyWindowsDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write permissive destination: %v", err)
	}
	if err := Write(path, validReceipt()); err != nil {
		t.Fatalf("write private receipt: %v", err)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("get current user token: %v", err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("get receipt security descriptor: %v", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("get receipt DACL: %v", err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("receipt DACL must contain exactly one ACE, got %#v", dacl)
	}

	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("get receipt ACL entry: %v", err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != windows.GENERIC_ALL {
		t.Fatalf("unexpected receipt ACL entry: type=%d mask=%#x", ace.Header.AceType, ace.Mask)
	}
	gotSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !user.User.Sid.Equals(gotSID) {
		t.Fatalf("receipt ACL SID = %s, want %s", gotSID.String(), user.User.Sid.String())
	}
}
