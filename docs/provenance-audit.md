# Provenance Integrity Revalidation and Portable Audit Bundle

P8 extends the P7 `provenance-backtrace/v1` read model. It does not add an evidence producer and does not change the persisted Task schema.

## Read-only actions

- `task_manage action=revalidate` rebuilds the complete passing Final Review provenance closure from authoritative Task, execution-receipt, receipt-claim, and (when configured) Evolution state.
- `task_manage action=export_audit_bundle` emits a bundle only when that live revalidation passes.
- `task_manage action=verify_audit_bundle` verifies a bundle without reading the live Task, execution-evidence, or Evolution stores.
- `agentdock audit verify <bundle.json> [--expect-sha256 <sha256>]` provides the same offline verifier as a process-level interface.

The Task persistence schema remains unchanged. P8 is a query/audit contract.

## Live integrity checks

A live revalidation fails closed when any part of the accepted provenance closure is no longer reproducible, including:

- stale or mismatched `review_revision`;
- a missing/non-completed bound Task step;
- requirement or evidence ownership drift;
- a missing, malformed, cross-task, or cross-step execution-receipt claim;
- an execution receipt whose recomputed `receipt_sha256` no longer matches its persisted step attestation;
- an Evolution evidence record whose Task or Final Review revision scope no longer matches;
- a dangling or contradictory graph node/edge/path produced from the authoritative state.

Opaque refs such as `artifact://`, `commit://`, and `test://` are verified as provenance identities owned by the Task step. P8 does not claim to re-hash the external artifact or Git object unless a dedicated authority already supplies such a digest.

## Portable bundle

`provenance-audit-bundle/v1` contains:

- the Task snapshot, including the passing Final Review provenance bindings;
- execution receipts and durable receipt-owner claims used by that review;
- matching Evolution records when the Evolution authority is available;
- a canonical `provenance-backtrace/v1` graph for the full review closure;
- a manifest containing component SHA-256 digests, graph counts, and the final `bundle_sha256`.

Graph nodes, edges, and paths are sorted deterministically before hashing. Receipt and claim snapshots are sorted by stable identity; Evolution snapshots are sorted by `evolution_id|revision`.

## Independent verification and trust boundary

The offline verifier recomputes component hashes, the bundle hash, receipt hashes, graph closure, Task/step ownership, receipt claims, and revision/scope relationships without consulting live stores.

The embedded `bundle_sha256` protects the bundle against changes where the attacker does not also rewrite the manifest. It is not an external trust anchor: an attacker who can replace the entire bundle can also recompute every embedded hash. For a portable bundle that must remain tamper-evident after it leaves AgentDock, store or transmit `bundle_sha256` through an independent channel and verify with `--expect-sha256` (or `expected_bundle_sha256` in the Task tool).

## P9 signed portable provenance

P9 adds an authentication layer above P8 without changing Task, execution-evidence, or Evolution write semantics. P8 remains the content-integrity authority; P9 answers a separate question: which AgentDock node authenticated this exact P8 bundle, and is that signer still trusted?

The P9 contracts are:

- `audit-signature-statement/v1`: the canonical statement signed by a node audit key;
- `audit-detached-signature/v1`: the Ed25519 detached signature plus its statement;
- `audit-trust-registry/v1`: a public root-signed registry that binds one Nexus node/device identity to its audit signing-key lifecycle;
- `provenance-trust/v1`: the combined P8 integrity + P9 trust-verification result.

`task_manage action=export_signed_audit_bundle` performs the normal P8 live revalidation first. Only a passing bundle is signed. It returns the portable bundle, detached signature, current public trust registry, `root_key_id`, and `trust_registry_revision`. `task_manage action=verify_signed_audit_bundle` is store-independent and requires the bundle, signature, public registry, an externally pinned `expected_root_key_id`, and an `expected_trust_revision` floor.

The process-level equivalents are:

```
agentdock audit trust init
agentdock audit trust status
agentdock audit trust export --out audit-trust.json
agentdock audit sign bundle.json --signature bundle.sig.json
agentdock audit verify-signed bundle.json \
  --signature bundle.sig.json \
  --trust-registry audit-trust.json \
  --trust-root audit-root-sha256:<hex> \
  --trust-revision <latest-known-revision>
```

The long-lived external trust anchor is the root key id plus a minimum known trust-registry revision. Unlike P8's per-bundle SHA pin, the root id authenticates many bundles and survives normal child-key rotation; the small monotonic revision changes only when signing-key lifecycle state changes.

### Trust model

The public key embedded in a registry is never self-authenticating. Verification fails closed unless the caller supplies the independently obtained `root_key_id` and a minimum known registry revision. The verifier recomputes the root-key fingerprint, rejects a registry older than that revision floor, verifies the root signature over the registry, verifies the child-key fingerprint and lifecycle state, verifies the detached Ed25519 signature, and then binds its `bundle_sha256`, Task id, and Final Review revision to the P8 bundle that was independently revalidated. This revision floor prevents replay of a valid root-signed registry captured before a later revocation.

The registry subject contains the paired Nexus `node_id` and `device_id`. Device tokens, OAuth secrets, and audit private keys are never exported in the portable trust material.

The local private store is `<AGENTDOCK_HOME>/audit-trust/private-v1.json`, written through AgentDock's private atomic-file path. It contains the audit root private key and only currently usable child signing keys. Public `audit-trust-registry/v1` exports contain public keys and lifecycle metadata only.

### Rotation and revocation

Normal rotation changes the active child key while retaining the same root trust anchor. The previous child becomes `retired`, its local private key is deleted, and signatures made at or before its `retired_at` remain verifiable from the public registry.

Revocation is the compromise-response path. A `revoked` child key invalidates every signature made with that key regardless of `signed_at`. Revoking the active key also creates a fresh active child key atomically, and the revoked private key is deleted locally.

P9 signatures contain a self-asserted `signed_at`; P9 does not provide an external trusted timestamp authority. Therefore rotation is a normal continuity mechanism, not a safe response to suspected key compromise: a stolen retired private key could otherwise backdate a newly forged statement. Use revocation when compromise is suspected.

Root-key compromise is intentionally not disguised as child-key rotation. Re-establishing a root trust anchor requires explicit rebootstrap and out-of-band redistribution of the new root pin.

### Backward compatibility

Unsigned `provenance-audit-bundle/v1` files and `agentdock audit verify --expect-sha256` remain valid P8 workflows. P9 adds authentication rather than changing P8 bundle hashing or persisted Task schema.
