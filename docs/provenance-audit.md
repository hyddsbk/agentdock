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

### Execution evidence namespace closure

`exec://` is a strong executor-receipt identity, not a generic opaque evidence URI. A Task checkpoint may persist `exec://` evidence only when AgentDock also persists the matching trusted executor attestation and receipt claim. Public `execution_evidence.evidence_refs` is therefore reserved for opaque outcome pointers such as `commit://`, `test://`, `artifact://`, or `issue://`; a naked `exec://` ref is rejected at checkpoint time with guidance to use `execution_receipts`.

This prevents a delayed integrity failure where a checkpoint appears valid but a later P8/P9 audit export discovers that the referenced receipt has no owner claim. Declared execution contracts and TDD receipt paths remain unchanged: their server-side receipt resolution/claiming path is the authority that turns an executor receipt into portable provenance.

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

The protected local private store is `<AGENTDOCK_HOME>/audit-trust/private-v2.dat`, written through AgentDock's private atomic-file path. It contains the audit root private key and only currently usable child signing keys. On Windows the serialized store is additionally protected with current-user DPAPI and a fixed AgentDock audit-trust entropy purpose before it reaches disk; on non-Windows the confidentiality boundary remains the private directory/file permissions. Upgrades from the original P9 `private-v1.json` format migrate the exact existing key material and registry into v2 under the trust-store lock, preserve `root_key_id` and registry revision, and remove the plaintext v1 file only after the protected v2 store is valid. Public `audit-trust-registry/v1` exports contain public keys and lifecycle metadata only.

### Rotation and revocation

Normal rotation changes the active child key while retaining the same root trust anchor. The previous child becomes `retired`, its local private key is deleted, and signatures made at or before its `retired_at` remain verifiable from the public registry.

Revocation is the compromise-response path. A `revoked` child key invalidates every signature made with that key regardless of `signed_at`. Revoking the active key also creates a fresh active child key atomically, and the revoked private key is deleted locally.

P9 signatures contain a self-asserted `signed_at`; P9 does not provide an external trusted timestamp authority. Therefore rotation is a normal continuity mechanism, not a safe response to suspected key compromise: a stolen retired private key could otherwise backdate a newly forged statement. Use revocation when compromise is suspected.

Root-key compromise is intentionally not disguised as child-key rotation. Re-establishing a root trust anchor requires explicit rebootstrap and out-of-band redistribution of the new root pin.

### Backward compatibility

Unsigned `provenance-audit-bundle/v1` files and `agentdock audit verify --expect-sha256` remain valid P8 workflows. P9 adds authentication rather than changing P8 bundle hashing or persisted Task schema.

## P10 verifier trust state and root continuity

P10 moves the P9 revision floor from a value that callers must remember to pass on every verification into a verifier-owned `audit-trust-state/v1` file. The state is stored at `<AGENTDOCK_HOME>/audit-trust/trust-state-v1.json` and contains only the paired subject, the current public root anchor, the highest accepted trust-registry revision, and an update timestamp. It contains no audit private key, Task evidence, or Evolution evidence.

Bootstrap remains an external trust decision:

```
agentdock audit trust-state bootstrap \
  --trust-registry audit-trust.json \
  --trust-root audit-root-sha256:<hex> \
  --trust-revision <out-of-band-known-revision>
```

After bootstrap, the verifier can use its persisted state instead of supplying the root pin and revision floor on every bundle:

```
agentdock audit verify-signed-stateful bundle.json \
  --signature bundle.sig.json \
  --trust-registry audit-trust.json
```

A stateful verification performs the complete P8 bundle integrity check and P9 signature/bundle/task/review binding before it changes trust state. Only a fully passing verification may advance `highest_accepted_registry_revision`. A later registry with a lower revision fails closed as `TRUST_STATE_ROLLBACK`; an invalid bundle or cross-scope signature never advances the state.

### Planned root rollover

Child-key rotation and revocation remain P9 lifecycle operations under one root. Planned root-key replacement is a separate P10 operation. The signer emits `audit-root-continuity/v1`, whose statement binds the subject, previous root and registry revision, next root and registry revision, and issue time. The same statement must verify under both the previous root and the next root. A new root therefore cannot authenticate its own succession without the currently trusted root.

```
agentdock audit trust rollover-root \
  --continuity root-continuity.json

agentdock audit trust export --out audit-trust-next.json

agentdock audit trust-state apply-rollover \
  --continuity root-continuity.json \
  --trust-registry audit-trust-next.json
```

Before the signer replaces its stored root, AgentDock atomically persists a local recovery copy at `<AGENTDOCK_HOME>/audit-trust/root-continuity-r<old>-r<new>.json`. This prevents an output-path failure from destroying the only bridge after the old root private key has been replaced. The verifier accepts rollover only when the continuity document starts exactly from its current anchor and highest accepted revision, both root signatures verify, and the supplied next registry is signed by and names the declared next root.

Suspected root compromise is not a planned rollover. It uses the explicit `agentdock audit trust-state rebootstrap ...` recovery path with a newly distributed out-of-band root pin and revision. Ordinary `bootstrap` refuses to overwrite existing verifier state.

P10 does not add a `task_manage` write action and does not modify Task, execution-evidence, or Evolution persistence. It changes only verifier trust-state persistence and process-level audit verification/authority-continuity commands.

### P10 backward compatibility

P9 explicit-pin `verify-signed` and `task_manage action=verify_signed_audit_bundle` remain valid and unchanged. P10 is additive: callers that do not opt into verifier trust state continue to use the P8/P9 contracts exactly as before.

## P11 trust checkpoint freshness

P11 adds bounded freshness without creating a new trust root. The signer may create an `audit-trust-checkpoint/v1` that binds its paired `node_id`/`device_id`, current root key id, exact trust-registry revision and SHA-256, plus `issued_at` and `fresh_until`. The checkpoint statement is signed by the current audit root and the freshness window is capped at 24 hours.

A checkpoint is only a freshness authority **under an already trusted P10 root**. Stateful verification requires an existing `audit-trust-state/v1`; checkpoint subject and root must exactly match that state, and the registry revision may not be older than `highest_accepted_registry_revision`. A checkpoint from another root fails closed and cannot bootstrap or replace verifier state. Planned root changes must first pass P10 `apply-rollover`; suspected compromise still requires explicit P10 `rebootstrap`.

Create or publish a checkpoint from the signer:

```
agentdock audit trust checkpoint create --fresh-for 6h --out checkpoint.json
agentdock audit trust checkpoint publish --fresh-for 6h --out checkpoint.json
```

Portable offline verification can use the root-signed snapshot with either an external P9 root pin or an existing P10 verifier state:

```
agentdock audit verify-signed bundle.json \
  --signature bundle.sig.json \
  --trust-root audit-root-sha256:<hex> \
  --trust-checkpoint checkpoint.json

agentdock audit verify-signed-stateful bundle.json \
  --signature bundle.sig.json \
  --trust-checkpoint checkpoint.json
```

Online verification fetches the latest checkpoint snapshot from the paired NexusDock and anchors it to local P10 verifier state:

```
agentdock audit verify-signed-online bundle.json \
  --signature bundle.sig.json
```

NexusDock stores only an authenticated node's latest monotonic checkpoint. It rejects lower revisions and same-revision equivocation, and it does not gain Task/Evolution evidence-writing authority or root-bootstrap authority. Only a fully passing checkpoint plus P8/P9 verification may advance the verifier's accepted registry revision; an expired or tampered checkpoint, or an invalid bundle, never advances state.
