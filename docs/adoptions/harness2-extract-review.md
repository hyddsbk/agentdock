# Harness² extraction review

Review date: 2026-10-23

Subject: `google-research/harness2`

Immutable source snapshot: `1cfe254e8d25b15f37048c3730134686abe60335`

Verdict: **EXTRACT**

## Approved units

- Propose–Probe–Compose as a Task-owned recursive improvement protocol.
- Parallel and sequential refinement modes.
- Separate base / invariant / project / alternative candidate scopes.
- Blind candidate evaluation using opaque aliases.
- Evidence-is-data isolation for candidate artifacts and probe evidence.
- Cost-aware harness surface placement: prefer on-demand Skill, deterministic helper, bounded subagent, or runtime-enforced gate where appropriate; always-on prose is last resort.
- Candidate-only handoff from a composed lesson into the existing Evolution `propose` lifecycle.

## Explicit exclusions

- Harness² executor/runtime and benchmark runners.
- Harness² installers, dependency bootstrap, dataset download and patch pipeline.
- OpenCode/Codex substrate integration from Harness².
- Harness² experience library as a persistent memory authority.
- Harness² orchestration, selector, or reflection components as competing AgentDock authorities.
- Any execution mode that disables AgentDock's existing permission, sandbox, execution-attestation, Task, Final Review, Evolution, Recall, or provenance boundaries.

## Architecture decision

AgentDock remains the sole owner of Task state, workflow admission, execution evidence, Final Review, Evolution lifecycle, Recall integration, and provenance. The extracted recursive-improvement protocol is an optional frozen sub-contract of a Task. It does not install or execute upstream Harness² code.

A composed reusable lesson is not automatically learned. It is returned only as an Evolution candidate handoff and must enter the existing `evolve propose` / pre-bound learning-check lifecycle for later support or contradiction.

No production switch is authorized by this review.
