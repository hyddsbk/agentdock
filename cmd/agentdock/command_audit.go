package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tasktool "github.com/uvwt/agentdock/internal/tool/task"
)

func runAuditCommand(args []string, stdout io.Writer) error {
	if len(args) < 2 || args[0] != "verify" {
		return errors.New("用法：agentdock audit verify <bundle.json|-> [--expect-sha256 <sha256>]")
	}
	path := strings.TrimSpace(args[1])
	if path == "" {
		return errors.New("audit bundle path is required")
	}
	expected := ""
	switch len(args) {
	case 2:
	case 4:
		if args[2] != "--expect-sha256" || strings.TrimSpace(args[3]) == "" {
			return errors.New("用法：agentdock audit verify <bundle.json|-> [--expect-sha256 <sha256>]")
		}
		expected = strings.TrimSpace(args[3])
	default:
		return errors.New("用法：agentdock audit verify <bundle.json|-> [--expect-sha256 <sha256>]")
	}

	var (
		reader io.Reader
		closef func() error
	)
	if path == "-" {
		reader = os.Stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open audit bundle: %w", err)
		}
		reader = file
		closef = file.Close
	}
	if closef != nil {
		defer closef()
	}

	var bundle tasktool.PortableAuditBundle
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&bundle); err != nil {
		return fmt.Errorf("decode audit bundle: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("audit bundle contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing audit bundle data: %w", err)
	}

	report := tasktool.VerifyPortableAuditBundleExpected(bundle, expected)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	if !report.Valid {
		return errors.New("audit bundle integrity verification failed")
	}
	return nil
}
