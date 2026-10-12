package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/audittrust"
	"github.com/uvwt/agentdock/internal/nexusbridge"
	tasktool "github.com/uvwt/agentdock/internal/tool/task"
)

func runAuditCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return auditUsageError()
	}
	switch args[0] {
	case "verify":
		return runAuditVerifyCommand(args[1:], stdout)
	case "sign":
		return runAuditSignCommand(args[1:], stdout)
	case "verify-signed":
		return runAuditVerifySignedCommand(args[1:], stdout)
	case "verify-signed-stateful":
		return runAuditVerifySignedStatefulCommand(args[1:], stdout)
	case "trust":
		return runAuditTrustCommand(args[1:], stdout)
	case "trust-state":
		return runAuditTrustStateCommand(args[1:], stdout)
	default:
		return auditUsageError()
	}
}

func runAuditVerifyCommand(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: agentdock audit verify <bundle.json|-> [--expect-sha256 <sha256>]")
	}
	path := strings.TrimSpace(args[0])
	if path == "" {
		return errors.New("audit bundle path is required")
	}
	expected := ""
	switch len(args) {
	case 1:
	case 3:
		if args[1] != "--expect-sha256" || strings.TrimSpace(args[2]) == "" {
			return errors.New("usage: agentdock audit verify <bundle.json|-> [--expect-sha256 <sha256>]")
		}
		expected = strings.TrimSpace(args[2])
	default:
		return errors.New("usage: agentdock audit verify <bundle.json|-> [--expect-sha256 <sha256>]")
	}

	bundle, err := readAuditBundle(path)
	if err != nil {
		return err
	}
	report := tasktool.VerifyPortableAuditBundleExpected(bundle, expected)
	if err := writeJSON(stdout, report); err != nil {
		return err
	}
	if !report.Valid {
		return errors.New("audit bundle integrity verification failed")
	}
	return nil
}

func runAuditSignCommand(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("agentdock audit sign", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	signaturePath := flags.String("signature", "", "detached signature output path")
	home := flags.String("home", "", "AgentDock home")
	if err := flags.Parse(reorderAuditFlagArgs(args)); err != nil || flags.NArg() != 1 || strings.TrimSpace(*signaturePath) == "" {
		return errors.New("usage: agentdock audit sign <bundle.json> --signature <signature.json> [--home <dir>]")
	}
	bundle, err := readAuditBundle(flags.Arg(0))
	if err != nil {
		return err
	}
	integrity := tasktool.VerifyPortableAuditBundle(bundle)
	if !integrity.Valid {
		if writeErr := writeJSON(stdout, integrity); writeErr != nil {
			return writeErr
		}
		return errors.New("refusing to sign an audit bundle that fails P8 integrity verification")
	}
	resolvedHome, err := resolveAuditHome(*home)
	if err != nil {
		return err
	}
	signature, err := audittrust.Sign(resolvedHome, audittrust.SignInput{
		BundleContract: bundle.Contract,
		BundleSHA256:   integrity.BundleSHA256,
		TaskID:         bundle.Manifest.TaskID,
		ReviewRevision: bundle.Manifest.ReviewRevision,
	}, time.Now())
	if err != nil {
		return err
	}
	data, err := audittrust.MarshalSignature(signature)
	if err != nil {
		return err
	}
	if err := writePublicFile(*signaturePath, data); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"contract":                signature.Contract,
		"signature_path":          filepath.Clean(*signaturePath),
		"bundle_sha256":           signature.Statement.BundleSHA256,
		"root_key_id":             signature.Statement.RootKeyID,
		"trust_registry_revision": signature.Statement.RegistryRevision,
		"key_id":                  signature.Statement.KeyID,
		"node_id":                 signature.Statement.NodeID,
		"device_id":               signature.Statement.DeviceID,
	})
}

func runAuditVerifySignedCommand(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("agentdock audit verify-signed", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	signaturePath := flags.String("signature", "", "detached signature path")
	registryPath := flags.String("trust-registry", "", "trusted audit registry path")
	rootKeyID := flags.String("trust-root", "", "externally pinned audit root key id")
	trustRevision := flags.Int("trust-revision", 0, "externally pinned minimum trust registry revision")
	expectedSHA := flags.String("expect-sha256", "", "optional expected P8 bundle SHA-256")
	if err := flags.Parse(reorderAuditFlagArgs(args)); err != nil || flags.NArg() != 1 || strings.TrimSpace(*signaturePath) == "" || strings.TrimSpace(*registryPath) == "" || strings.TrimSpace(*rootKeyID) == "" || *trustRevision <= 0 {
		return errors.New("usage: agentdock audit verify-signed <bundle.json> --signature <signature.json> --trust-registry <registry.json> --trust-root <root_key_id> --trust-revision <revision> [--expect-sha256 <sha256>]")
	}
	bundle, err := readAuditBundle(flags.Arg(0))
	if err != nil {
		return err
	}
	signature, err := audittrust.LoadSignatureFile(*signaturePath)
	if err != nil {
		return err
	}
	registry, err := audittrust.LoadRegistryFile(*registryPath)
	if err != nil {
		return err
	}
	report := tasktool.VerifySignedPortableAuditBundleExpected(bundle, signature, registry, *rootKeyID, strings.TrimSpace(*expectedSHA), *trustRevision, time.Now())
	if err := writeJSON(stdout, report); err != nil {
		return err
	}
	if !report.Valid {
		return errors.New("signed audit bundle trust verification failed")
	}
	return nil
}

func runAuditVerifySignedStatefulCommand(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("agentdock audit verify-signed-stateful", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	signaturePath := flags.String("signature", "", "detached signature path")
	registryPath := flags.String("trust-registry", "", "trusted audit registry path")
	home := flags.String("home", "", "AgentDock home containing verifier trust state")
	expectedSHA := flags.String("expect-sha256", "", "optional expected P8 bundle SHA-256")
	if err := flags.Parse(reorderAuditFlagArgs(args)); err != nil || flags.NArg() != 1 || strings.TrimSpace(*signaturePath) == "" || strings.TrimSpace(*registryPath) == "" {
		return errors.New("usage: agentdock audit verify-signed-stateful <bundle.json> --signature <signature.json> --trust-registry <registry.json> [--home <dir>] [--expect-sha256 <sha256>]")
	}
	bundle, err := readAuditBundle(flags.Arg(0))
	if err != nil {
		return err
	}
	signature, err := audittrust.LoadSignatureFile(*signaturePath)
	if err != nil {
		return err
	}
	registry, err := audittrust.LoadRegistryFile(*registryPath)
	if err != nil {
		return err
	}
	resolvedHome, err := resolveAuditHome(*home)
	if err != nil {
		return err
	}
	report, state, err := tasktool.VerifySignedPortableAuditBundleWithTrustState(resolvedHome, bundle, signature, registry, strings.TrimSpace(*expectedSHA), time.Now())
	if err != nil {
		return err
	}
	output := map[string]any{
		"verification": report,
		"trust_state":  state,
	}
	if err := writeJSON(stdout, output); err != nil {
		return err
	}
	if !report.Valid {
		return errors.New("stateful signed audit bundle trust verification failed")
	}
	return nil
}

func runAuditTrustCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return auditTrustUsageError()
	}
	switch args[0] {
	case "init":
		flags := flag.NewFlagSet("agentdock audit trust init", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 0 {
			return errors.New("usage: agentdock audit trust init [--home <dir>]")
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		identity, err := nexusbridge.Load(resolvedHome)
		if err != nil {
			return fmt.Errorf("load paired Nexus node identity: %w", err)
		}
		registry, err := audittrust.Init(resolvedHome, audittrust.Subject{NodeID: identity.NodeID, DeviceID: identity.DeviceID}, time.Now())
		if err != nil {
			return err
		}
		return writeTrustSummary(stdout, registry)
	case "rotate":
		flags := flag.NewFlagSet("agentdock audit trust rotate", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 0 {
			return errors.New("usage: agentdock audit trust rotate [--home <dir>]")
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		registry, err := audittrust.Rotate(resolvedHome, time.Now())
		if err != nil {
			return err
		}
		return writeTrustSummary(stdout, registry)
	case "rollover-root":
		flags := flag.NewFlagSet("agentdock audit trust rollover-root", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		continuityPath := flags.String("continuity", "", "root continuity output path")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 0 || strings.TrimSpace(*continuityPath) == "" {
			return errors.New("usage: agentdock audit trust rollover-root --continuity <continuity.json> [--home <dir>]")
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		registry, continuity, err := audittrust.RolloverRoot(resolvedHome, time.Now())
		if err != nil {
			return err
		}
		data, err := audittrust.MarshalRootContinuity(continuity)
		if err != nil {
			return err
		}
		if err := writePublicFile(*continuityPath, data); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{
			"contract":                      registry.Contract,
			"revision":                      registry.Revision,
			"node_id":                       registry.Subject.NodeID,
			"device_id":                     registry.Subject.DeviceID,
			"root_key_id":                   registry.RootKeyID,
			"active_key_id":                 audittrust.ActiveKeyID(registry),
			"root_continuity_contract":      continuity.Contract,
			"root_continuity_path":          filepath.Clean(*continuityPath),
			"root_continuity_recovery_path": audittrust.RootContinuityRecoveryPath(resolvedHome, continuity),
		})
	case "revoke":
		flags := flag.NewFlagSet("agentdock audit trust revoke", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 1 {
			return errors.New("usage: agentdock audit trust revoke <key_id> [--home <dir>]")
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		registry, err := audittrust.Revoke(resolvedHome, flags.Arg(0), time.Now())
		if err != nil {
			return err
		}
		return writeTrustSummary(stdout, registry)
	case "status":
		flags := flag.NewFlagSet("agentdock audit trust status", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 0 {
			return errors.New("usage: agentdock audit trust status [--home <dir>]")
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		registry, err := audittrust.RegistryFromHome(resolvedHome)
		if err != nil {
			return err
		}
		return writeTrustSummary(stdout, registry)
	case "export":
		flags := flag.NewFlagSet("agentdock audit trust export", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		out := flags.String("out", "-", "registry output path or -")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 0 {
			return errors.New("usage: agentdock audit trust export [--home <dir>] [--out <registry.json|->]")
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		registry, err := audittrust.RegistryFromHome(resolvedHome)
		if err != nil {
			return err
		}
		data, err := audittrust.MarshalRegistry(registry)
		if err != nil {
			return err
		}
		if *out == "-" {
			_, err = stdout.Write(data)
			return err
		}
		return writePublicFile(*out, data)
	default:
		return auditTrustUsageError()
	}
}

func runAuditTrustStateCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return auditTrustStateUsageError()
	}
	switch args[0] {
	case "bootstrap", "rebootstrap":
		command := args[0]
		flags := flag.NewFlagSet("agentdock audit trust-state "+command, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		registryPath := flags.String("trust-registry", "", "trusted audit registry path")
		rootKeyID := flags.String("trust-root", "", "externally pinned audit root key id")
		trustRevision := flags.Int("trust-revision", 0, "externally pinned minimum trust registry revision")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 0 || strings.TrimSpace(*registryPath) == "" || strings.TrimSpace(*rootKeyID) == "" || *trustRevision <= 0 {
			return fmt.Errorf("usage: agentdock audit trust-state %s --trust-registry <registry.json> --trust-root <root_key_id> --trust-revision <revision> [--home <dir>]", command)
		}
		registry, err := audittrust.LoadRegistryFile(*registryPath)
		if err != nil {
			return err
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		var state audittrust.TrustState
		if command == "bootstrap" {
			state, err = audittrust.BootstrapTrustState(resolvedHome, registry, *rootKeyID, *trustRevision, time.Now())
		} else {
			state, err = audittrust.RebootstrapTrustState(resolvedHome, registry, *rootKeyID, *trustRevision, time.Now())
		}
		if err != nil {
			return err
		}
		return writeJSON(stdout, state)
	case "status":
		flags := flag.NewFlagSet("agentdock audit trust-state status", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 0 {
			return errors.New("usage: agentdock audit trust-state status [--home <dir>]")
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		state, err := audittrust.LoadTrustState(resolvedHome)
		if err != nil {
			return err
		}
		return writeJSON(stdout, state)
	case "apply-rollover":
		flags := flag.NewFlagSet("agentdock audit trust-state apply-rollover", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		home := flags.String("home", "", "AgentDock home")
		continuityPath := flags.String("continuity", "", "root continuity document path")
		registryPath := flags.String("trust-registry", "", "next trusted audit registry path")
		if err := flags.Parse(reorderAuditFlagArgs(args[1:])); err != nil || flags.NArg() != 0 || strings.TrimSpace(*continuityPath) == "" || strings.TrimSpace(*registryPath) == "" {
			return errors.New("usage: agentdock audit trust-state apply-rollover --continuity <continuity.json> --trust-registry <registry.json> [--home <dir>]")
		}
		continuity, err := audittrust.LoadRootContinuityFile(*continuityPath)
		if err != nil {
			return err
		}
		registry, err := audittrust.LoadRegistryFile(*registryPath)
		if err != nil {
			return err
		}
		resolvedHome, err := resolveAuditHome(*home)
		if err != nil {
			return err
		}
		state, err := audittrust.ApplyRootContinuity(resolvedHome, continuity, registry, time.Now())
		if err != nil {
			return err
		}
		return writeJSON(stdout, state)
	default:
		return auditTrustStateUsageError()
	}
}

func readAuditBundle(path string) (tasktool.PortableAuditBundle, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return tasktool.PortableAuditBundle{}, errors.New("audit bundle path is required")
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
			return tasktool.PortableAuditBundle{}, fmt.Errorf("open audit bundle: %w", err)
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
		return tasktool.PortableAuditBundle{}, fmt.Errorf("decode audit bundle: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return tasktool.PortableAuditBundle{}, errors.New("audit bundle contains multiple JSON values")
		}
		return tasktool.PortableAuditBundle{}, fmt.Errorf("decode trailing audit bundle data: %w", err)
	}
	return bundle, nil
}

func resolveAuditHome(explicit string) (string, error) {
	if value := strings.TrimSpace(explicit); value != "" {
		return filepath.Clean(value), nil
	}
	if value := strings.TrimSpace(os.Getenv("AGENTDOCK_HOME")); value != "" {
		return filepath.Clean(value), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errors.New("resolve AgentDock home for audit trust")
	}
	return filepath.Join(home, ".agentdock"), nil
}

func writePublicFile(path string, data []byte) error {
	path = strings.TrimSpace(path)
	if path == "" || path == "-" {
		return errors.New("output file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create audit output directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write audit output: %w", err)
	}
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeTrustSummary(stdout io.Writer, registry audittrust.Registry) error {
	return writeJSON(stdout, map[string]any{
		"contract":      registry.Contract,
		"revision":      registry.Revision,
		"node_id":       registry.Subject.NodeID,
		"device_id":     registry.Subject.DeviceID,
		"root_key_id":   registry.RootKeyID,
		"active_key_id": audittrust.ActiveKeyID(registry),
		"key_count":     len(registry.Keys),
	})
}

func auditUsageError() error {
	return errors.New("usage: agentdock audit <verify|sign|verify-signed|verify-signed-stateful|trust|trust-state> ...")
}

func auditTrustUsageError() error {
	return errors.New("usage: agentdock audit trust <init|status|rotate|rollover-root|revoke|export> ...")
}

func auditTrustStateUsageError() error {
	return errors.New("usage: agentdock audit trust-state <bootstrap|status|apply-rollover|rebootstrap> ...")
}

// reorderAuditFlagArgs lets the audit CLI accept the documented spelling
// where a positional bundle/key may appear before its flags. P9 audit flags
// all take one value, so each option moves together with its value while
// positionals keep their relative order.
func reorderAuditFlagArgs(args []string) []string {
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			flagArgs = append(flagArgs, arg)
			if strings.Contains(arg, "=") {
				continue
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flagArgs, positionals...)
}
