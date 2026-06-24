//go:build windows

package syncshared

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func prepareFileBeforeWrite(path string, mode os.FileMode) error {
	if !isPrivateFileMode(mode) {
		return nil
	}
	return applyFileMode(path, mode)
}

func applyFileMode(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	if !isPrivateFileMode(mode) {
		return nil
	}
	if err := applyOwnerOnlyACL(path); err != nil {
		return fmt.Errorf("set owner-only ACL: %w", err)
	}
	return nil
}

func isPrivateFileMode(mode os.FileMode) bool {
	return mode.Perm() == 0o600
}

func applyOwnerOnlyACL(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("get current user SID: %w", err)
	}
	userSID, err := user.User.Sid.Copy()
	if err != nil {
		return fmt.Errorf("copy current user SID: %w", err)
	}

	access := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(userSID),
		},
	}}
	acl, err := windows.ACLFromEntries(access, nil)
	if err != nil {
		return fmt.Errorf("build owner-only ACL: %w", err)
	}

	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}
