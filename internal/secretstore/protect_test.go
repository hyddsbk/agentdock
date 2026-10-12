package secretstore

import (
	"bytes"
	"testing"
)

func TestProtectRoundTripAndPurposeBinding(t *testing.T) {
	plain := []byte("sensitive-audit-private-store")
	protected, err := Protect(plain, "audit-purpose-a")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, plain) {
		t.Fatalf("protected payload contains plaintext: %q", protected)
	}
	roundTrip, err := Unprotect(protected, "audit-purpose-a")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(roundTrip, plain) {
		t.Fatalf("round trip = %q", roundTrip)
	}
	if _, err := Unprotect(protected, "audit-purpose-b"); err == nil {
		t.Fatal("protected payload accepted the wrong purpose")
	}
}
