//go:build windows

package securepath

import (
	"fmt"
	"os"
	"sort"
	"unsafe"

	"golang.org/x/sys/windows"
)

// EnsurePrivate installs a protected DACL that grants full control only to the
// current user, SYSTEM and local administrators. Directory entries inherit the
// same boundary to newly-created children.
func EnsurePrivate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows user SID: %w", err)
	}
	inheritance := ""
	if info.IsDir() {
		inheritance = "OICI"
	}
	sddl := fmt.Sprintf(
		"D:P(A;%s;FA;;;%s)(A;%s;FA;;;SY)(A;%s;FA;;;BA)",
		inheritance,
		user.User.Sid.String(),
		inheritance,
		inheritance,
	)
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("build private Windows DACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private Windows DACL: %w", err)
	}
	current, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read current Windows DACL for %s: %w", path, err)
	}
	if current != nil {
		matches, matchErr := equivalentProtectedDACL(current, descriptor)
		if matchErr != nil {
			return fmt.Errorf("compare current Windows DACL for %s: %w", path, matchErr)
		}
		if matches {
			return nil
		}
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("secure private Windows path %s: %w", path, err)
	}
	return nil
}

func equivalentProtectedDACL(current, desired *windows.SECURITY_DESCRIPTOR) (bool, error) {
	control, _, err := current.Control()
	if err != nil {
		return false, err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return false, nil
	}
	currentEntries, err := allowedDACLEntries(current)
	if err != nil {
		return false, err
	}
	desiredEntries, err := allowedDACLEntries(desired)
	if err != nil {
		return false, err
	}
	if len(currentEntries) != len(desiredEntries) {
		return false, nil
	}
	for i := range currentEntries {
		if currentEntries[i] != desiredEntries[i] {
			return false, nil
		}
	}
	return true, nil
}

func allowedDACLEntries(sd *windows.SECURITY_DESCRIPTOR) ([]string, error) {
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, err
	}
	if dacl == nil {
		return nil, nil
	}
	entries := make([]string, 0, int(dacl.AceCount))
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return nil, err
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return nil, nil
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		entries = append(entries, fmt.Sprintf(
			"%d:%d:%08x:%s",
			ace.Header.AceType,
			ace.Header.AceFlags,
			uint32(ace.Mask),
			sid.String(),
		))
	}
	sort.Strings(entries)
	return entries, nil
}
