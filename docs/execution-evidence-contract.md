# P5 — Execution Evidence / Verification Provenance Contract

## Scope

P5 generalizes the executor-issued receipt mechanism introduced by P4 from
TDD-only evidence into an opt-in Task/Workflow execution contract.

It is not a standalone Skill and it does not replace P3 traceability:

- P3 `execution_evidence.evidence_refs` remains available for compact pointers
  such as commit, test, artifact, issue, or other implementation evidence.
- P4 `tdd_test_command` remains the specialized Red → Green → Regression gate.
- P5 `execution_contract` is the generic gate for build, deploy, migration,
  release, verification, smoke-test, and similar command-backed steps.

A single Task step cannot declare both a TDD seam and a generic execution
contract. This keeps one completion authority per step.

## Declaration

A Task or Workflow Template step may declare:

```json
{
  "execution_contract": {
    "ordered": true,
    "checks": [
      {
        "id": "build",
        "command": "go build ./...",
        "allowed_exit_codes": [0]
      },
      {
        "id": "verify",
        "command": "go test ./...",
        "allowed_exit_codes": [0]
      }
    ]
  }
}
```

At concrete Task creation the Task state layer freezes:

- the check id;
- the exact normalized command;
- the command SHA-256;
- the allowed exit-code set (default: `[0]`);
- whether checks are ordered;
- a server-generated `declared_at` timestamp.

The declaration timestamp cannot be supplied or replaced by checkpoint input.

## Completion

A declared generic execution contract must be completed through a single-step
checkpoint. The caller supplies only a mapping from declared check ids to
executor receipt references:

```json
{
  "execution_receipts": [
    {"check_id": "build", "ref": "exec://session-..."},
    {"check_id": "verify", "ref": "exec://session-..."}
  ]
}
```

The Task service resolves each receipt from the shared execution-evidence store
and fails closed unless every declared check is represented exactly once and:

1. the receipt exists and passes receipt integrity validation;
2. the issuer is `agentdock.exec_command/v1`;
3. the exact command and command digest match the frozen check;
4. the execution started after the Task declaration;
5. the process terminated with status `exited`;
6. the run did not time out and was not killed;
7. the exit code belongs to the declared `allowed_exit_codes`;
8. receipt references are distinct;
9. when `ordered=true`, every earlier check finishes before the next begins.

The caller cannot inject executor identity, exit code, timestamps, command
digest, output hashes, or attestation payloads through the public
`execution_evidence` field.

## Provenance and anti-reuse

Resolved receipts are durably claimed under the same
`task:<task_id>:step:<step_id>` ownership model used by P4.

- A receipt already claimed by another Task step is rejected.
- Duplicate receipt references within one completion are rejected.
- Retries for the same Task step are idempotent.
- Claims created by a failed checkpoint are rolled back.
- Invalid receipts do not mutate the Task.
- Batch checkpoint cannot bypass a pending step with an execution contract.

The persisted Task evidence contains the compact evidence refs plus executor
attestations containing the receipt-bound command, exit status, timing,
runtime/workdir, stdout/stderr digests, and receipt digest. Raw command output
is not duplicated into Task state.

## Traceability interaction

When a step also maps P3 `requirement_refs`, the attested `exec://` refs are
stored in the step's execution evidence and therefore contribute to the same
requirement-coverage calculation. Optional legacy evidence refs can be supplied
alongside executor receipts when a step also needs commit/artifact/spec lineage.

A step without `execution_contract` retains legacy P3 behavior and is not
silently upgraded to an executor gate.

## Workflow propagation

`TemplateStep.execution_contract` is copied into the concrete Task step.
The concrete Task then generates a new `declared_at` timestamp and freezes
command digests. Templates therefore define the policy, while concrete Tasks
own the execution-time freshness boundary.

## Task state schema

P5 advances Task state from schema v8 to **v9**.

The reader accepts v1 through v9. Existing v1-v8 files are upgraded to v9 in
memory only; there is no bulk rewrite. A legacy Task is persisted as v9 only
after a normal state mutation.

This preserves the P0-P4 dependency, context, quality, diagnostic, handoff,
traceability, two-axis review, and TDD fields while keeping rollback practical.

## Trust boundary

P5 is **EXECUTOR-ATTESTED / FRESH / POLICY-BOUND / SINGLE-USE** inside the
AgentDock runtime boundary.

The receipt digest is an integrity mechanism, not a secret-key signature.
A host administrator or process with unrestricted write access to AgentDock
state remains outside this threat model. Filesystem permissions, process
identity, deployment controls, and host/container isolation remain the security
boundary.

## Release gate

Acceptance requires:

- unit tests for declaration normalization and completion validation;
- negative tests for missing, stale, wrong-command, wrong-exit, duplicate,
  reused, unordered, timed-out, or non-normal receipt evidence;
- tests proving explicitly allowed nonzero exits work;
- Workflow Template propagation tests;
- v8 → v9 lazy migration coverage;
- P0-P4 targeted regressions;
- full repository tests and vet;
- copied-production-state read validation;
- exact candidate-binary end-to-end execution-contract proof;
- a rollback snapshot before any production switch.
