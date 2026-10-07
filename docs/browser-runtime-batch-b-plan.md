# Browser Runtime Batch B — Implementation and Release Gates

Baseline: Batch A commit e9e4796ec02ca8c52c826969562b6411506ddad0.
Working branch: feat/browser-runtime-batch-b.
Production Core remains bd92f80 on 127.0.0.1:8765. An isolated, recoverable Batch A canary runs on 127.0.0.1:18765.
Batch A evidence: C:\Users\18372\AgentDock\reports\2026-10-08-browser-batch-a-gray-rollout.md.

## 1. Observed source baseline
- Browser actions and navigation have operation/context timeouts already (actions.go).
- Session cleanup is currently explicit through browser_session(action=cleanup_stale), default stale age six hours (service_sessions.go). Do not mistake this for automatic watchdog behavior.
- Owned vs external Chromium cleanup differs: external CDP sessions must release AgentDock targets without terminating external Chrome (session.go).
- Model-supplied CDP endpoints are already restricted to loopback IPs (cdp_discovery.go).
- Browser start and goto URL inputs do not yet have an explicit centralized navigation-scheme policy; goto feeds user URL to CDP Navigate (tool_parse.go / actions.go).
- Batch A loop detection signals replan_required but does not automatically issue replacement navigation/actions.

## 2. First Batch B slice — BrowserWatchdog
- Track per-session last successful CDP activity and consecutive recoverable timeout/transport errors; export structured status, not secrets.
- Safe, bounded periodic sweep for *owned* stale sessions; avoid races with active calls. Do not kill external Chromium.
- Preserve manual cleanup_stale behavior; add idempotent and observable recovery/cleanup.
- Never silently replay side-effecting click, fill or navigation actions.
- Tests: last-activity updates; stale vs active session; browser crash / lost CDP; timeout; external CDP target-only release; concurrent close/cleanup; no orphan or resurrection.

## 3. Second Batch B slice — BrowserSecurityPolicy
- Explicit, versioned navigation URL scheme policy: http/https and about:blank (the sole intentional special-case) unless a trusted config explicitly expands it.
- Apply policy to initial URL, goto, storage-origin injection and redirect boundaries; reject javascript:, file:, data:, chrome: and malformed / credential-bearing URLs where appropriate. Distinguish explicit local user test servers from remote destinations.
- Keep current CDP URL loopback-only guard and prohibit arbitrary CDP endpoints from crossing trust boundaries.
- Preserve safe public HTTPS browsing and loopback developer fixture tests. No hardcoded blanket RFC1918 ban without product-level authority.
- Tests: allowed HTTPS/http/loopback and about:blank, rejected unsafe schemes, encoded/relative input, navigation redirects, storage-origin and CDP scope, no browser teardown on policy rejection.

## 4. Gates
- Commit incremental changes with focused tests before gray deployment.
- Use existing offline Go module/build caches, with browser_integration-tagged real Chromium tests.
- Run live isolated AgentDock MCP session/start/act/snapshot/close, verify Watchdog and SecurityPolicy outputs and failures.
- Prove revert (canary port offline; main 8765 healthy and Nexus connected; SHA unchanged) and restore.
- DO NOT update Windows Desktop production runtime or external MCP schema as part of source development. Formal rollout and ChatGPT schema refresh/new session are separate gates.

Status at creation: baseline audited, implementation pending.

