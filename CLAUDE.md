# CLAUDE.md — lfx-v2-email-service

Development guide for Claude instances working on this service.

> **Central LFX skills:**
>
> - `lfx-skills:lfx`: cross-repo topology, ownership routing, repo discovery, and missing-checkout handling.
> - `lfx-skills:lfx-platform-architecture`: platform composition, service classes, NATS/KV ownership, Helm and ArgoCD handoffs, and cross-service responsibility boundaries.
>
> Repo-local skills live in `.claude/skills/` and are invoked from this repo:
>
> - `/email-service-dev` auto-attaches on Go, chart, and service-owned doc paths. It owns this repo's Go conventions, NATS request/reply handler shape, public `pkg/api` contract, SMTP/SES/SQS tracking behavior, KV tracking rules, tests, formatting, linting, and license headers.
> - `/email-service-pr-readiness` checks PR shape only: branch, JIRA, conventional commits, rebase status, DCO + GPG signing, diff size, and protected files.
> - `/email-service-preflight` runs the mechanical Go pre-PR pipeline: working tree, license headers, formatting, lint, build, tests, protected files, commit verification, and change summary.
> - `/email-service-code-reviewer` and `/email-service-learnings-reviewer` are the repo-owned reviewer brains loaded by the background review subagents described under **Pre-PR review** below — they are not invoked by hand.
>
> If the plugin is missing, install with `/plugin marketplace add linuxfoundation/lfx-skills` then `/plugin install lfx-skills@lfx-skills`.

## Service Overview

Thin NATS request/reply relay. Receives pre-rendered `{to, subject, html, text, from?, from_display_name?, reply_to?}`
payloads and delivers them via Amazon SES SMTP. No templates, no template registry —
callers are responsible for rendering their own content.

The optional `from` field lets callers override the sender address per message; the domain
must be in `SMTP_ALLOWED_FROM_DOMAINS` (default: `lfx.linuxfoundation.org`). The optional
`from_display_name` overrides the display name in the From header (default: `"LFX Self Serve"`).
The optional `reply_to` field sets the SMTP `Reply-To` header; the domain must be in the
reply-to allowlist (`SMTP_ALLOWED_REPLY_TO_DOMAINS`, default: `linuxfoundation.org`, subdomain
suffix matching — so `lfx.linuxfoundation.org` is also permitted).

Recipient domain filtering is centralized here via `SMTP_ALLOWED_RECIPIENT_DOMAINS` (default:
empty = permit all). Set in non-prod environments to prevent test notifications from reaching
real users' personal addresses. Subdomain suffix matching applies; a blocked recipient returns an
empty success response so callers don't log expected non-prod filtering as a delivery failure.

**Technologies:** Go 1.24, NATS (`nats.go`), `net/smtp`, Kubernetes/Helm

## Repo Role

This repo owns transactional email delivery over NATS request/reply, the email-service public Go contract in `pkg/api`, NATS KV engagement tracking, SES/SQS engagement event handling, and the service-local Helm chart. It does not own template rendering, newsletter composition, newsletter persistence, FGA tuple emission, or indexer publishing.

## Authoritative Repo Docs

- `docs/email-service-contract.md`: public NATS subjects, payloads, response shapes, errors, and tracking record fields.
- `docs/email-engagement-tracking.md`: SES configuration set header, tracking header, SQS poller, event handling, and KV update behavior.
- `docs/service-helm-chart.md`: service-local chart values, secrets, NATS KV bucket CRs, and deployment handoffs.
- `charts/lfx-v2-email-service/`: service-local Helm templates and defaults.

Read the relevant contract before changing `pkg/api`, NATS handlers, tracking fields, SES/SQS behavior, or chart values. Update docs in the same PR as behavior changes.

## Consumed Cross-Repo Contracts

- Shared service chart conventions: `lfx-v2-helm/docs/service-chart-patterns.md`
- Deployed values, image tags, IRSA annotations, ExternalSecret wiring: `lfx-v2-argocd`
- Newsletter caller and future newsletter integration: `lfx-v2-newsletter-service`

Use `lfx-skills:lfx` if an owner repo is missing locally, a path has moved, or the task needs additional peer repos.

## Architecture

Clean layered architecture:

```
cmd/email-service/                  → entry point, wiring, config
internal/domain/                    → interfaces (Sender, TrackingStore, AddressPolicy, NullTrackingStore)
internal/service/                   → NATS message handlers (SendEmail, GetEmailStatus, GetEmailEngagementAnalytics, EngagementEvent)
internal/service/mocks/             → test doubles (mocks.TrackingStore satisfies domain.TrackingStore)
internal/infrastructure/kv/         → KV adapter (kv.Store implements domain.TrackingStore; owns JSON marshaling, CAS retry, group fan-out)
internal/infrastructure/smtp/       → SMTPSender, NoOpSender, MIME builder
internal/infrastructure/sqs/        → SQS long-poll loop (feeds EngagementEventHandler)
internal/infrastructure/nats/       → NATS tracing helpers (ExtractAndStartConsumerSpan)
internal/infrastructure/observability/ → OpenTelemetry SDK setup (traces, metrics, logs via autoexport)
internal/logging/                   → structured log helpers (AppendCtx, ErrKey, InitStructuredLogConfig)
pkg/api/                            → PUBLIC: NATS subjects, KV bucket names, wire types (callers import this)
pkg/redaction/                      → email address redaction for logs
```

### Key design decisions

- **Pre-rendered only.** No template engine. Callers send HTML + plain text.
- **pkg/api is the public contract.** Any service that wants to send email imports
  `github.com/linuxfoundation/lfx-v2-email-service/pkg/api` for the subject constant
  and `SendEmailRequest` type. Never expose `internal/` packages to callers.
- **`pkg/constants` must not be recreated.** The subject string `"lfx.email-service.send"` is
  stale and does **not** match the live subject (`"lfx.email-service.send_email"`). This package
  does **not exist in the repository** — do not create it. All subject constants, queue
  group names, and KV bucket names are canonically defined in `pkg/api/nats.go`.
- **NoOpSender for local dev.** `EMAIL_ENABLED` defaults to `false` (NoOpSender logs
  instead of sending). Set `EMAIL_ENABLED=true` to enable real SMTP delivery.
- **Queue group for horizontal scaling.** The subscription uses queue group
  `lfx.email-service.queue` so each message is delivered to exactly one pod.
- **All four subjects are always subscribed.** Status and analytics handlers are subscribed unconditionally at startup. When NATS KV is unavailable, `NullTrackingStore` is wired in and all reads return `ErrNotFound`, which the handlers map to a `"not found"` error reply. Never skip a subscription based on KV availability — callers must not hang on `RequestWithContext`.
- **Handle always responds.** The NATS handler calls `msg.Respond` on every path
  (success → JSON `SendEmailResponse`, failure → JSON `SendEmailErrorResponse`) so
  callers' `RequestWithContext` never hangs.
- **30-second SMTP bounded wait.** `SMTPSender.Send` runs `smtp.SendMail` in a goroutine and waits up to 30 seconds (`smtpTimeout` constant in `internal/infrastructure/smtp/sender.go`). If the deadline fires, `Send` returns an error to the caller; the underlying network connection may continue briefly in the background goroutine until the OS-level TCP timeout fires.
  Do not add outer retries that ignore this timeout — they will compound rather than bound latency.

## Development Workflow

### Build version injection

`make build` and `make run` pass `-ldflags` that inject three variables into the `main` package at link time:

| Variable | Source | Default (no build) |
|---|---|---|
| `main.Version` | `git describe --tags --always --dirty` | `"dev"` |
| `main.BuildTime` | `date -u` at build time | `"unknown"` |
| `main.GitCommit` | `git rev-parse --short HEAD` | `"unknown"` |

These are injected at link time only. Do not add runtime version-detection logic — the injected values are the canonical source of truth. Do not strip the `LDFLAGS` variable from the Makefile.

### Prerequisites

- Go 1.24+
- `nats` CLI (`brew install nats-io/nats-tools/nats`)
- Docker (for local NATS + Mailpit)

### Common tasks

```bash
make build            # compile binary to bin/email-service
make run              # build and run with env vars from shell
make test             # go test -race ./...
make test-verbose     # go test -race -v ./...
make test-coverage    # test with coverage report (HTML in coverage/)
make lint             # golangci-lint run
make fmt              # go fmt + gofmt -s (no goimports)
make check            # gofmt check + lint + license-check (does not run tests)
make license-check    # standalone license-header check for all .go files
make docker-build     # build Docker image (ghcr.io/linuxfoundation/lfx-v2-email-service/email-service)
make helm-install       # helm upgrade --install (defaults; requires cluster access)
make helm-install-local # helm upgrade --install with values.local.yaml overlay
make helm-templates     # render chart templates to stdout (no cluster needed)
make helm-uninstall     # helm uninstall from lfx namespace
make helm-restart       # kubectl rollout restart the deployment
```

## Pre-PR review

Before a PR exists, local review uses the same three reviewers in two modes: **post-commit review** while development continues, and one **full-branch review** immediately before opening the PR.

Every review batch launches exactly THREE generic background subagents together, all with `subagent_type: general-purpose`, `model: opus` (Opus 5), and `run_in_background: true`. At most one batch may be active. The reviewers load exactly one skill each:

1. `/lfx-skills:lfx-general-code-review`
2. `/email-service-code-reviewer`
3. `/email-service-learnings-reviewer`

The reviewers only report findings. They never edit tracked files, stage, commit, push, or write GitHub state; the parent performs all changes.

### Shared reviewer prompt

Give each reviewer one complete prompt. Start with its loading policy, then append the common instructions.

- General: `Load /lfx-skills:lfx-general-code-review with the Skill tool. If that skill is unavailable, do not review unguided and do not read a replacement SKILL.md from any checkout or cache; return INCOMPLETE.`
- Repo code: `Load /email-service-code-reviewer with the Skill tool. If and only if that skill is unavailable in this child's current session, locate the lfx-v2-email-service repo root and read <repo-root>/.claude/skills/email-service-code-reviewer/SKILL.md. Follow that file as the sole review guidance. Do not search another path or use another skill or agent. If the file is missing, return INCOMPLETE.`
- Repo learnings: `Load /email-service-learnings-reviewer with the Skill tool. If and only if that skill is unavailable in this child's current session, locate the lfx-v2-email-service repo root and read <repo-root>/.claude/skills/email-service-learnings-reviewer/SKILL.md. Follow that file as the sole review guidance. Do not search another path or use another skill or agent. If the file is missing, return INCOMPLETE.`

```text
target repo: lfx-v2-email-service
repo root: <absolute repo root>
target_sha: <full target SHA>
base_sha: <full base SHA>
review exactly: git diff <full base SHA> <full target SHA>
range label: <mode-specific range label>

The repo root and SHA range above are authoritative. Do not re-derive the range from HEAD or origin/main. If the assigned skill tells you to derive the review range or changed-file list from HEAD, git show, or origin/main, replace that instruction with the exact pinned git diff above. Read added or modified code from <target_sha>:<path>, deleted code from <base_sha>:<path>, and both revisions for a rename. Never use a moving working-tree copy as code evidence. Load current rule, contract, checklist, architecture, and knowledge-base policy as the assigned skill directs.

Report findings only. Follow the assigned skill's report conventions and return its complete findings. Prepend `Reviewed range: <full base SHA>..<full target SHA>`, then `Skill: /lfx-skills:lfx-general-code-review`, `Skill: /email-service-code-reviewer`, or `Skill: /email-service-learnings-reviewer`, matching that reviewer. If a repo reviewer used its allowed file fallback, append `; read from: <exact path>` to its Skill line. If incomplete, put `INCOMPLETE — <reason>` first, then the same two verification lines.
```

Accept a batch only when all three reviewers return non-empty, complete reports for the pinned full-SHA range, name their exact assigned `/...` skill, and report no unauthorized fallback path. If any reviewer fails these checks, reject the entire batch; never accept or rerun only one reviewer.

### Mode 1 — Post-commit review

Use this mode after normal development commits while work continues.

1. Commit with `git commit -s -S`.
2. Maintain `reviewed_through_sha`: the latest commit fully covered by an accepted post-commit batch. Before the first batch, initialize it to the parent of the first pending commit. Never advance it for a failed or incomplete batch.
3. When no batch is active, set `base_sha=$reviewed_through_sha` and `target_sha=$(git rev-parse HEAD)`. Label a one-commit range `the latest commit`; if commits accumulated, label it `the commits since the last review`.
4. Launch the three reviewers together with that exact range. If another batch is already active, let it finish; the next batch will cover everything from the unchanged `reviewed_through_sha` through the then-current `HEAD`.
5. While remaining in Mode 1, if the batch is invalid and `HEAD` is unchanged, rerun all three with the same pins. If `HEAD` changed, rerun all three over the coalesced range from the unchanged `reviewed_through_sha` through current `HEAD`. Once work moves to Mode 2, do not rerun an invalid post-commit batch; Mode 2's whole-branch review replaces its coverage.
6. After a valid batch, advance `reviewed_through_sha` to its `target_sha`. Verify its findings against current code and address every Critical and reasonable Important finding in a later commit; that commit is reviewed by the next post-commit batch.
7. The final planned commit skips post-commit review and moves directly to Mode 2. Leave `reviewed_through_sha` unchanged. If development resumes before Mode 2 starts, the next post-commit batch covers the entire pending range from that unchanged SHA.

### Mode 2 — Full-branch review before opening the PR

Entering this mode ends post-commit review for this PR attempt. Finish any active post-commit batch and retain every finding that Mode 1 requires the parent to address. Do not retry an invalid post-commit batch; the whole-branch review below replaces its coverage. Do not return to Mode 1.

1. Run `git fetch origin`, set `target_sha=$(git rev-parse HEAD)` and `base_sha=$(git merge-base origin/main HEAD)`, and launch the three reviewers together once against the whole branch range. Use the shared prompt with the range label `the branch's diff against origin/main` and review `git diff <full base SHA> <full target SHA>`. Never use `reviewed_through_sha` for this review.
2. If the batch is operationally incomplete, it does not count as the review. Without editing files or creating commits, repeat step 1 so the unchanged branch is fetched, re-pinned, and reviewed by a complete three-reviewer batch until one valid result returns.
3. Fix the retained post-commit findings and the issues raised by the whole-branch review, then complete the repository's documentation-currency updates. Commit all resulting changes with `git commit -s -S`, then run `/email-service-pr-readiness` and `/email-service-preflight` against the clean, committed `HEAD`. If either check requires fixes, apply the remedy appropriate to the finding—rewrite local commits for existing-history defects or create a new signed/DCO commit for file changes—then rerun the affected deterministic checks. Ensure every resulting commit is signed and carries DCO sign-off. Do not run the local reviewers again.
4. Push and open the PR. From that point onward, use Post-PR review only.

## Post-PR review

Once the PR exists, never run the local post-commit reviewers or another local full-branch review. PR iteration uses Copilot and every other configured GitHub code-review agent/bot.

1. After every push, wait for the configured GitHub reviewers to finish reviewing the current head, then enumerate every unresolved review thread. Collect compatible feedback into a batch rather than making one-comment-at-a-time commits.
2. Work in an isolated background task when safe so the developer can continue. Never allow two writers to edit the same worktree or race commits or pushes; otherwise handle the feedback synchronously.
3. Verify every finding against the current head, actual runtime/API contracts, repository guidance, and approved PR scope. Never assume a bot is correct and never silently ignore a finding.
4. For a genuine in-scope issue, make the smallest focused fix and validate it. Otherwise, tell the developer why and post an evidence-backed rebuttal. Escalate architecture, security, ownership, and excluded-surface questions instead of guessing.
5. Comment before resolving every thread. For a fix, cite the fix commit and validation evidence; for a rebuttal, give the reason and evidence. Every thread must end fixed-and-explained or rebutted-and-explained.
6. Group compatible fixes into one signed/DCO commit, push, wait for reviews on the new head, and repeat until no unresolved actionable threads remain and required checks are green.
7. Do not merge as part of this automated iteration. Merge only after a separate explicit human instruction.

## Docs currency checklist

**Do not open a PR until `CLAUDE.md` and all relevant `docs/` files match the code on the branch.**

#### CLAUDE.md sections to verify

| Changed area | CLAUDE.md section(s) to check |
|---|---|
| New or removed file in `internal/domain/` | **Architecture** layout, **Testing Patterns** |
| New or removed file in `internal/infrastructure/` | **Architecture** layout |
| New or removed file in `internal/service/` or `internal/service/mocks/` | **Architecture** layout, **Testing Patterns** |
| Any handler constructor signature change | **Testing Patterns** (mock references, `HandleData` shape) |
| New NATS subject or KV bucket | **NATS Subjects**, **NATS KV**, **Adding a New NATS Subject** |
| Any `cmd/email-service/main.go` wiring change | **Key design decisions** |
| New, removed, or renamed env variable; default changed | **Environment Variables** table |
| New design decision or invariant | **Key design decisions** |
| New, removed, or renamed skill under `.claude/skills/`, or a change to the reviewer launch model | **Central LFX skills** bullet list, **Pre-PR review** |

#### docs/ files to verify

| Changed area | File |
|---|---|
| NATS subject, payload shape, response, error string | `docs/email-service-contract.md` |
| KV bucket, tracking record field, engagement event handling | `docs/email-engagement-tracking.md` |
| Helm value, secret name, NATS KV bucket CR | `docs/service-helm-chart.md` |

#### What counts as stale

- A type, interface, struct, or package exists in code but is absent from the Architecture layout.
- A mock, test helper, or test pattern reference points to a deleted or renamed symbol.
- A design decision or invariant changed in code but is not recorded in **Key design decisions**.
- Conditional behavior was removed or added (e.g. "subjects are conditionally subscribed" → "always subscribed") but the doc still says the old thing.
- An env variable was added, renamed, or had its default changed without an update to the **Environment Variables** table.
- A skill body or description names a reviewer, agent, skill, or file path that no longer exists. A reference retained for documented pinned-source equivalence is not stale while its target still exists.

## Keeping the PR title and description accurate

Use the standard PR title format:

`<type>(<scope>): <summary> [<ticket>]`

Types: `feat` | `fix` | `refactor` | `docs` | `chore`. Scope is optional but recommended. Ticket
reference is optional — include `[LFXV2-XXXX]` when a ticket exists; omit the bracket entirely
when there is no ticket. Do not use a placeholder like `[LFXV2-0000]`.

After every `git push` to an open PR, verify the title and description are still accurate:

1. Read the current PR: `gh pr view <number> --json title,body`.
2. Compare against the actual branch diff: `git diff origin/main...HEAD --stat` and `git log --oneline origin/main..HEAD`.
3. If the title no longer reflects the primary change, update it: `gh pr edit <number> --title "..."`.
4. If the description is missing new changes, omits updated protected files, references removed work, or contains stale notes, update it: `gh pr edit <number> --body "..."`.

Do not leave a PR with a title or description that contradicts or understates what is actually in the branch. Reviewers rely on the description to understand scope; a stale description causes confusion and unnecessary review comments.

## Local dev loop

```bash
# Terminal 1: NATS
docker run --rm -p 4222:4222 nats:latest

# Terminal 2: Mailpit (SMTP capture, UI at :8025)
docker run --rm -p 1025:1025 -p 8025:8025 axllent/mailpit

# Terminal 3: service
SMTP_HOST=localhost SMTP_PORT=1025 EMAIL_ENABLED=true \
  NATS_URL=nats://localhost:4222 make run
```

### Send a test message

```bash
# Default sender
nats req lfx.email-service.send_email \
  '{"to":"test@example.com","subject":"Hello","html":"<p>Hi</p>","text":"Hi"}'

# Custom from address + display name
nats req lfx.email-service.send_email \
  '{"to":"test@example.com","subject":"Hello","html":"<p>Hi</p>","text":"Hi","from":"events@lfx.linuxfoundation.org","from_display_name":"LFX Events"}'
```

## NATS Subjects

| Constant | Value | Direction |
|---|---|---|
| `api.SendEmailSubject` | `lfx.email-service.send_email` | request/reply; reply is JSON `SendEmailResponse` |
| `api.QueueGroup` | `lfx.email-service.queue` | queue group for all subscriptions |
| `api.GetEmailStatusSubject` | `lfx.email-service.get_email_status` | request/reply; payload `GetEmailStatusRequest` → `EmailRecipientRecord` for `email_id`, `[]EmailRecipientRecord` for `group_id` |
| `api.GetEmailEngagementAnalyticsSubject` | `lfx.email-service.get_email_engagement_analytics` | request/reply; payload `GetEmailEngagementAnalyticsRequest` → `GetEmailEngagementAnalyticsResponse` |

All constants are in `pkg/api/nats.go`.

> **Do not create `pkg/constants`.** The subject string `"lfx.email-service.send"` is stale
> and does not match the live subject. This package does not exist in the repository — do not
> create it. All subject constants are in `pkg/api/nats.go`.

## NATS KV

| Constant | Bucket | Key | Value |
|---|---|---|---|
| `api.EmailRecipientsKVBucket` | `email-recipients` | `<email_id>` (UUID per send) | JSON `EmailRecipientRecord` |
| `api.EmailGroupIndexKVBucket` | `email-group-index` | `<group_id>` (UUID per campaign) | JSON `[]string` of `email_id`s |

The `email_id` and `group_id` are returned to callers in `SendEmailResponse`.
The `group_id` is optional in `SendEmailRequest` — if not provided the email service generates one.

## SES Engagement Event Processing

SES delivers engagement events via SNS → SQS. The SQS poller (`internal/infrastructure/sqs`) passes each message to `EngagementEventHandler.Handle`.

**Event flow:**
1. SQS message body is a JSON SNS envelope: `{"MessageId": "<sns-id>", "Message": "<ses-event-json>"}`.
2. The inner SES event JSON contains `eventType` and `mail.headers`.
3. The handler reads the `X-LFX-TRACKING-ID` header (format: `<group_id>/<email_id>`), splits on the last `/` to extract `email_id`, then looks up the `EmailRecipientRecord` in the `email-recipients` KV bucket.

**Handled event types** (all others are silently dropped):

| SES `eventType` | Effect on `EmailRecipientRecord` |
|---|---|
| `OPEN` | Sets `Opened=true`, appends to `OpenedAtList` (deduplicated by SNS `MessageId`), increments `OpenCount`, updates `LastOpenedAt` |
| `DELIVERY` | Sets `Delivered=true`, records `DeliveredAt` (first delivery only) |
| `BOUNCE` | Sets `Failed=true`, records `FailedAt` (first failure only) |
| `COMPLAINT` | Sets `Failed=true`, records `FailedAt` (first failure only) |

**Open-event deduplication:** SNS may redeliver the same event. Each `OPEN` entry stores the SNS `MessageId` as `EventID`; the handler skips any open event whose `MessageId` is already in `OpenedAtList`.

**KV write conflict retry:** the handler retries the `KeyValue.Update` once on any update error before giving up and returning an error (which keeps the SQS message in-flight for redelivery).

## Environment Variables

| Variable | Default | Notes |
|---|---|---|
| `NATS_URL` | `nats://localhost:4222` | |
| `PORT` | `8080` | HTTP health probe port |
| `EMAIL_ENABLED` | `false` | `true`/`t`/`1` → SMTPSender; anything else → NoOpSender |
| `SMTP_HOST` | `localhost` | |
| `SMTP_PORT` | `587` | STARTTLS |
| `DEFAULT_SMTP_FROM` | `noreply@lfx.linuxfoundation.org` | service-level default sender address; takes priority over the legacy `SMTP_FROM` fallback below |
| `SMTP_FROM` | _(empty)_ | **Legacy fallback only — do not set in new deployments.** Read only when `DEFAULT_SMTP_FROM` is unset, so existing deployments that haven't migrated yet don't silently revert to the hardcoded default. Migrate to `DEFAULT_SMTP_FROM`. |
| `DEFAULT_SMTP_FROM_DISPLAY_NAME` | `LFX Self Serve` | display name in the From header when no per-message `from_display_name` is set |
| `SMTP_ALLOWED_FROM_DOMAINS` | `lfx.linuxfoundation.org` | comma-separated list of domains permitted for per-message `from` overrides; set to `""` to block all per-message overrides |
| `SMTP_ALLOWED_REPLY_TO_DOMAINS` | `linuxfoundation.org` | comma-separated base domains for `reply_to`; subdomains also permitted; set to `""` to block |
| `SMTP_ALLOWED_RECIPIENT_DOMAINS` | _(empty — permit all)_ | comma-separated base domains permitted as recipients (subdomain suffix matching); empty = permit all (production default); set in non-prod to block real users |
| `SMTP_USERNAME` | _(empty)_ | From K8s Secret in production |
| `SMTP_PASSWORD` | _(empty)_ | From K8s Secret in production |
| `SES_EVENTING_ENABLED` | `false` | `true`/`t`/`1` → start the SQS engagement event poller; fatal at startup if AWS config fails to load |
| `SES_CONFIGURATION_SET` | _(empty)_ | SES v2 configuration set name; when set adds `X-SES-CONFIGURATION-SET` header to outbound mail |
| `SES_ENGAGEMENT_SQS_QUEUE_URL` | _(empty)_ | SQS queue URL for SES engagement events; required when `SES_EVENTING_ENABLED=true` |
| `LOG_LEVEL` | `info` | |
| `LOG_ADD_SOURCE` | `false` | `true` → include file/line in log entries |
| `OTEL_TRACES_EXPORTER` | `otlp` | OTel span exporter; `none` disables tracing |
| `OTEL_METRICS_EXPORTER` | `otlp` | OTel metrics exporter; `none` disables metrics |
| `OTEL_LOGS_EXPORTER` | `otlp` | OTel log exporter; `none` disables OTel log bridge |
| `OTEL_TRACES_SAMPLER` | `parentbased_traceidratio` | Sampler type; supports `always_on`, `always_off`, `traceidratio`, `parentbased_*` variants |
| `OTEL_TRACES_SAMPLER_ARG` | `1.0` | Ratio argument for ratio-based samplers (0.0–1.0) |
| `OTEL_PROPAGATORS` | `tracecontext,baggage` | Comma-separated propagator names (via `autoprop`) |
| `OTEL_SERVICE_NAME` | `lfx-v2-email-service` | Service name in trace/metric metadata |
| `OTEL_SERVICE_VERSION` | _(empty)_ | Set via `app.otel.serviceVersion` in Helm values; empty by default (not auto-derived from the image tag) |

## Testing Patterns

- **Table-driven tests** in `_test.go` files co-located with source.
- **All tests run with `-race`** (`TEST_FLAGS=-race` in the Makefile). New tests must
  be safe under the race detector; avoid shared mutable state without synchronization.
- **`mockSender`** in `internal/service/send_email_handler_test.go` — satisfies `domain.Sender`.
- **`mocks.TrackingStore`** in `internal/service/mocks/tracking.go` — a thread-safe
  in-memory mock that satisfies `domain.TrackingStore`. Construct with
  `mocks.NewTrackingStore()` and pre-seed records with `PutRecord` / `PutGroup`.
  `WriteErr`, `AppendErr`, `GetErrFor`, and `GroupErrFor` inject errors for specific
  conditions. Use this for all handler tests that touch KV tracking — do not write a
  new tracking mock.
- **`HandleData`** on `SendEmailHandler` and `GetEmailStatusHandler` — testable entry
  point that takes raw bytes and a respond callback; `Handle` wraps it for real NATS
  messages. Use `HandleData` in tests instead of embedding a real NATS server.
- **`EngagementEventHandler.Handle`** takes an `sqs/types.Message` (not raw bytes). Its
  test shape differs from the other handlers: construct the handler, call `Handle` directly
  with a crafted `types.Message`, and assert KV side-effects via `mocks.TrackingStore`. See
  `internal/service/engagement_event_handler_test.go` for the pattern.
- **Package `smtp` tests** use the unexported `buildEmailMessage` / `generateMessageID`
  / `generateBoundary` helpers directly (internal test package `package smtp`).

## Adding a New NATS Subject

1. Add the subject constant and any new wire types to `pkg/api/nats.go`.
2. Add a handler struct in `internal/service/` following the `SendEmailHandler` pattern.
3. Register the `QueueSubscribe` in `cmd/email-service/main.go`.
4. Add a table-driven test for `HandleData`.
5. **Start a consumer span in the `QueueSubscribe` callback** in `cmd/email-service/main.go`
   using `nats.ExtractAndStartConsumerSpan` from `internal/infrastructure/nats`, and pass
   the resulting `spanCtx` to `Handle`. Call `span.End()` (deferred) in the same callback.
   Do not start the span inside `Handle` or `HandleData` — `HandleData` has no `*natsgo.Msg`
   for header extraction, and starting it in `Handle` would duplicate spans since `main.go`
   already creates one before calling `Handle`. This propagates distributed traces from
   callers through to the handler. Omitting this step silently breaks cross-service
   traces for the new subject.

```go
// In the QueueSubscribe callback in main.go:
spanCtx, span := natstracing.ExtractAndStartConsumerSpan(msgCtx, msg, api.YourNewSubject)
defer span.End()
yourHandler.Handle(spanCtx, msg)
```

## Code Conventions

- `slog.DebugContext` for success paths, `slog.WarnContext` for recoverable issues,
  `slog.ErrorContext` for unexpected failures.
- **Always use `logging.ErrKey` as the slog key for error values** — never bare string
  literals like `"err"` or `"error"`. `logging.ErrKey` is the canonical constant
  (`"error"`) that keeps log field names consistent across the service.

  ```go
  slog.ErrorContext(ctx, "failed to send", logging.ErrKey, err)  // correct
  slog.ErrorContext(ctx, "failed to send", "err", err)           // wrong
  ```

- **Use `logging.AppendCtx(ctx, slog.Attr)` to attach sticky fields to a context.**
  Any attribute added this way is automatically included in every slog record that
  uses that context — useful for per-request IDs, email IDs, group IDs, and other
  fields that should appear on every log line in a handler call without having to
  pass them explicitly.

  ```go
  ctx = logging.AppendCtx(ctx, slog.String("email_id", emailID))
  // all subsequent slog.*Context calls with ctx will include email_id
  ```

- Redact email addresses in log fields: `redaction.RedactEmail(addr)`.
- Pass `context.Context` as the first argument; never store it in a struct.
- Binaries go to `bin/` — never to the repo root.
- NATS payload types belong in `pkg/api/` (public). Domain interfaces in
  `internal/domain/`. Infrastructure in `internal/infrastructure/`.

## Helm Chart

`charts/lfx-v2-email-service/` ships with the Go code in the same PR.

SMTP credentials are **not** in the chart. They come from a Kubernetes Secret
created out-of-band (terraform / sealed-secrets), referenced in the Deployment
via `valueFrom.secretKeyRef`. The Secret name is configurable via
`values.yaml` → `app.email.smtpSecretName` (default: `lfx-v2-email-service`; set to `""` to skip credential injection for local dev).

The Secret must have keys `smtp-username` and `smtp-password`.

## License

Copyright The Linux Foundation and each contributor to LFX.
SPDX-License-Identifier: MIT
