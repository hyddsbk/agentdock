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
