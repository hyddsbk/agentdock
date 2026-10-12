---
name: skill-vetter-runtime
description: Review ClawHub or local Skill packages before installation, classify risk, and return a structured security report.
version: 0.1.8
---

# Skill Vetter

This Skill provides a security-first review workflow and an optional helper script for inspecting ClawHub slugs or local Skill directories.

Use it before installing unknown skills, when comparing candidate skills, or when checking whether a local Skill has suspicious code, broad permissions, credential access, persistence hooks, or risky network behavior.

The output is a structured JSON report with reviewed files, detected red flags, permission clues, risk level, verdict, and notes.

## Upstream adoption-plan boundary

When work arrives from `project-due-diligence` with a validated `project-due-diligence/adoption-plan/v1`, preserve its `plan_sha256` and treat it as a non-expandable scope boundary. This Skill may vet only units permitted by that plan and must not reintroduce anything named in `excluded_components`.

The plan must allow `skill-vetter-runtime` as a downstream owner. If the proposed package contains an excluded runtime, installer, updater, authority, dependency, or component, stop and return the scope conflict to `project-due-diligence`; do not treat a successful package vet as permission to widen the adoption boundary.

When the plan is execution-receipt enabled, a completed vet must be represented by a `project-due-diligence/execution-receipt/v1` `vet-package` receipt. Use the exact package SHA-256 as both input/output artifact digest because vetting must not mutate the package, and bind the structured vet report with `result_sha256`. For ADAPT this receipt must extend the existing author/govern chain; for ADOPT it may be the first receipt but must still bind a concrete immutable package digest. Do not hand off to installation unless the receipt chain verifies successfully.


## Local path boundary

vet-local-path is portable and fail-closed. It must not discover or assume AgentDock private directories.

- allowed_roots may be supplied as a non-empty JSON array of explicit directory/file roots.
- If allowed_roots is omitted, the only allowed root is the current working directory supplied by the host.
- allow_outside_roots: true is an explicit escape hatch for a caller that has already authorized the target path.
- The legacy allow_outside_agentdock boolean is accepted only as a compatibility alias for allow_outside_roots; it does not cause any AgentDock path discovery.
- Error/status output may show the effective allowed roots, but never reads secrets or environment files merely to discover a host layout.
## 辅助脚本执行

Skill 本体是本说明文档。确需调用包内辅助脚本时，在 Skill 包根目录使用相对路径执行；运行宿主负责切换到包根目录并把所需变量注入当前子进程。

```bash
printf '%s' '{"skill_action":"<动作>"}' | python3 run.py
```

输入必须是 JSON 对象。写操作仍按本文档中的确认规则执行。

| 动作 | 用途 |
|---|---|
| `status` | Check Python runtime and optional ClawHub CLI availability. |
| `vet-clawhub-slug` | Fetch and vet a ClawHub skill by slug without installing it. |
| `vet-local-path` | Vet a local skill folder. Local reads are confined to caller-supplied allowed_roots, or the current working directory when none are supplied. |
