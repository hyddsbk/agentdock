# SEO Release Gate

P1.3 turns the SEO layering and runtime-hygiene rules into a centralized package release gate.

## Scope

The gate applies to the top-level `seo` router and every `seo-*` Skill. Non-SEO Skills retain the existing package behavior.

The gate runs in the native Skill manager, not only in the MCP wrapper:

- `validate`: returns every violation as a structured issue.
- `install`: blocks before content is copied into the installed Skill store.
- `activate`: rechecks the installed package before switching the active version.
- `rollback`: rechecks the previous installed version before reactivation.

A release-gate failure uses code `SKILL_RELEASE_GATE_FAILED` and a stage of the form
`release_gate.seo.<rule>`.

## Enforced rules

| Rule stage | Invariant |
| --- | --- |
| `provider_authority` | Evidence/measurement providers may retrieve and normalize evidence, but may not claim strategy, priority, Page Contract, URL-ownership, or implementation authority. |
| `page_contract_bypass` | A Skill that declares production page/content work must name both the Page Contract and `rui-website-plan` authority before production. |
| `legacy_runtime` | Legacy `claude-seo`, Claude command/Skill runtime paths, or equivalent historical runtime references are rejected from executable/instructional package content. |
| `fixed_path` | Host-specific Windows, POSIX, or home-relative paths are rejected; Skills must use Skill-relative resources or runtime-resolved paths. |
| `self_install_dependency` | Skills may not run their own package-manager/bootstrap installation commands at runtime. Dependencies belong to the managed runtime/package environment. |
| `duplicate_authority` | SEO Skills may consume or hand off to existing authorities, but may not duplicate URL/IA/Page Contract, keyword-prioritization, shared-domain-language, or consequential user-decision ownership. |

Legal metadata such as `LICENSE*`, `COPYING*`, and `NOTICE*` is excluded from runtime-reference scanning so upstream attribution is preserved without becoming executable architecture.

## Authority map protected by the gate

- `rui-website-plan`: URL ownership, IA/site architecture, canonical ownership, internal-link graph, page roles, and Page Contracts.
- `keyword-research`: keyword universe and keyword prioritization.
- `domain-modeling`: shared domain semantics, concepts, glossary, and terminology.
- `decision-grilling`: consequential unresolved user-owned decisions.
- `seo-google`, `seo-dataforseo`, `seo-maps`: evidence/measurement providers only.

## Regression contract

The focused test suite includes positive and negative fixtures for all six rule families, verifies that non-SEO packages are unaffected, verifies that legal attribution is not treated as runtime architecture, verifies `validate` returns structured issues, and verifies `install` and `activate` cannot bypass the gate.

An opt-in source-tree regression test uses `AGENTDOCK_SEO_SKILLS_ROOT` to run the exact same gate across the current `seo` and `seo-*` Skill corpus. This is the release baseline for future SEO Skill additions and upgrades.


## P1.4 CI and release integration

The CI, tagged Release, and Windows Installer workflows have a dedicated
`seo-release-gate` job. Release platform build jobs and the Windows
Installer validation job depend on it. The job checks out the external
`uvwt/agentdock-skills` corpus, rejects missing or incomplete SEO Skill
packages, and executes Go tests for all gate rules, the full corpus, and
install/validate/activate integration.

**Release is intentionally blocked until the authoritative P1.3 SEO corpus
is committed to the external Skill repository.** A local installed Skill
directory is not a substitute for a versioned CI input. Publish and review
the corpus before attempting a tag release, and record its commit SHA with
the release evidence. Do not bypass the preflight or treat a skipped corpus
test as a green gate.

To verify locally with a matching corpus:

```sh
AGENTDOCK_SEO_SKILLS_ROOT=/absolute/path/to/agentdock-skills/skills \
  go test ./internal/skill -run '^Test(SEOReleaseGate|ValidateReportsSEOReleaseGateIssues|InstallCannotBypassSEOReleaseGate|ActivateRechecksSEOReleaseGate)' -count=1 -v
```

After a green CI run, build a new Core from the tested commit and deploy
using the installed platform's supported installer/update mechanism.
Capture the previous binary/release identifier and persistent state
backup before switching; verify version, MCP connection, Skill indexing,
and release-gate negative cases after switching. Do not claim deployment
complete until health checks pass and rollback is independently verified.


### P1.4.1 corpus provenance

CI checks out the **exact** 40-character Skill corpus commit
`174b190a83789f085488f9e56d0c2b9e3ef7cbe8`. This commit exists
only in the local `agentdock-skills-release-sync` worktree as of this
preparation; it is **not** yet available on the remote. This deliberately
causes remote jobs to fail closed until the reviewed corpus commit has
been published. Do not replace the SHA with floating `main` or skip
this job to make the build green.

Local push attempt failed because no usable GitHub write credentials
were available. After push/merge, update the pin only when the reviewed
commit changes and rerun the full release gate.
