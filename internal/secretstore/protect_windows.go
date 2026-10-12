//go:build windows

package secretstore

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsPrefix = "dpapi-v1:"

func Protect(data []byte, purpose string) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("secret payload is empty")
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return nil, errors.New("secret purpose is required")
	}
	entropy := []byte(purpose)
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	optionalEntropy := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &optionalEntropy, 0, nil, 0, &output); err != nil {
		return nil, fmt.Errorf("protect secret with DPAPI: %w", err)
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	protected := append([]byte(nil), unsafe.Slice(output.Data, int(output.Size))...)
	encoded := base64.RawStdEncoding.EncodeToString(protected)
	return []byte(windowsPrefix + encoded + "\n"), nil
}

func Unprotect(payload []byte, purpose string) ([]byte, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return nil, errors.New("secret purpose is required")
	}
	text := strings.TrimSpace(string(payload))
	if !strings.HasPrefix(text, windowsPrefix) {
		return nil, errors.New("secret payload does not use DPAPI v1")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(text, windowsPrefix))
	if err != nil || len(ciphertext) == 0 {
		return nil, errors.New("secret DPAPI payload is invalid")
	}
	entropy := []byte(purpose)
	input := windows.DataBlob{Size: uint32(len(ciphertext)), Data: &ciphertext[0]}
	optionalEntropy := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, &optionalEntropy, 0, nil, 0, &output); err != nil {
		return nil, fmt.Errorf("unprotect secret with DPAPI: %w", err)
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return append([]byte(nil), unsafe.Slice(output.Data, int(output.Size))...), nil
}
