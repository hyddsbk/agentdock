//go:build windows

package securepath

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestEquivalentProtectedDACLAllowsEquivalentOrderAndAutoInheritedControl(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	desired, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := windows.SecurityDescriptorFromString(
		"D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")",
	)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := equivalentProtectedDACL(current, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !matches {
		t.Fatal("equivalent protected DACLs should match regardless of ACE order or auto-inherited control")
	}
}

func TestEquivalentProtectedDACLRejectsUnprotectedOrExtraACE(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	desired, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, sddl := range map[string]string{
		"unprotected": "D:(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
		"extra ace":   "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FR;;;BU)",
	} {
		t.Run(name, func(t *testing.T) {
			current, err := windows.SecurityDescriptorFromString(sddl)
			if err != nil {
				t.Fatal(err)
			}
			matches, err := equivalentProtectedDACL(current, desired)
			if err != nil {
				t.Fatal(err)
			}
			if matches {
				t.Fatalf("DACL %q should not match the private target", sddl)
			}
		})
	}
}
