//go:build !windows

package secretstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const unixPrefix = "private-file-v1:"

func Protect(data []byte, purpose string) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("secret payload is empty")
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return nil, errors.New("secret purpose is required")
	}
	sum := sha256.Sum256([]byte(purpose))
	return []byte(unixPrefix + hex.EncodeToString(sum[:]) + ":" + base64.RawStdEncoding.EncodeToString(data) + "\n"), nil
}

func Unprotect(payload []byte, purpose string) ([]byte, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return nil, errors.New("secret purpose is required")
	}
	text := strings.TrimSpace(string(payload))
	if !strings.HasPrefix(text, unixPrefix) {
		return nil, errors.New("secret payload does not use private-file v1")
	}
	parts := strings.SplitN(strings.TrimPrefix(text, unixPrefix), ":", 2)
	if len(parts) != 2 {
		return nil, errors.New("secret private-file payload is invalid")
	}
	sum := sha256.Sum256([]byte(purpose))
	if parts[0] != hex.EncodeToString(sum[:]) {
		return nil, errors.New("secret purpose does not match payload")
	}
	raw, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(raw) == 0 {
		return nil, errors.New("secret private-file payload is invalid")
	}
	return raw, nil
}
