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
> - `email-service-code-reviewer` and `email-service-learnings-reviewer` are the repo-owned reviewer brains loaded by the work cycle's background review subagents (see **Work cycle** below) — they are not invoked by hand. The `local-code-review` and `local-learnings-review` directory symlinks beside them are stable discovery aliases for the same two skills.
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

## Work cycle — post-commit and pre-PR reviews

> **CRITICAL — while the branch is pre-PR, post-commit review is mandatory.** After every commit on the local branch, launch **three generic background review subagents in one parallel batch** via the Agent tool — every child `subagent_type: general-purpose`, `model: opus`, `run_in_background: true`. Each child explicitly loads exactly one review skill: (1) `lfx-skills:lfx-general-code-review`, (2) this repo's `email-service-code-reviewer`, (3) this repo's `email-service-learnings-reviewer` — then keep working while they run. Before opening a PR, every running review must return clean (or remaining findings explicitly documented as trade-offs), the **full-branch sweep** must run clean if the branch has more than one commit (`branch` keyword), AND `/email-service-pr-readiness` must clear every Critical finding before `/email-service-preflight` runs.
>
> **Once the PR is open, do NOT invoke these pre-PR reviewers on iteration commits.** Copilot + `github-license-compliance[bot]` auto-trigger on every push and own the audit surface from that point (CodeRabbit is not enabled on this repo). The general, email-service, and learnings reviewers are pre-PR insurance only.

### Post-commit (pre-PR phase, after every commit, asynchronous)

1. **Commit your work.** `git commit -s -S`. Do not wait for any prior review to finish.
2. **Immediately launch all three reviewer subagents in one parallel batch.** All three are generic children — `subagent_type: general-purpose`, `model: opus`, `run_in_background: true` — sent in a single message so they run concurrently. Each child's prompt tells it to load exactly one review skill and follow it against the shared review inputs (step 3):
   - Child 1 loads `lfx-skills:lfx-general-code-review`.
   - Child 2 loads this repo's `email-service-code-reviewer` skill (read `.claude/skills/email-service-code-reviewer/SKILL.md` directly if the Skill tool does not list it).
   - Child 3 loads this repo's `email-service-learnings-reviewer` skill (read `.claude/skills/email-service-learnings-reviewer/SKILL.md` directly if the Skill tool does not list it).
3. **Shared review inputs — identical for all three children (exact).** Pin the range once, before launching, and pass every child the same values:

   ```text
   target repo: lfx-v2-email-service
   target_sha: <git rev-parse HEAD>
   base_sha: <git rev-parse HEAD^>
   review exactly: <base_sha>..<target_sha>

   Review the latest commit.
   ```

   Append `extra: <focus>` on a new line only when there is a priority hint to add. Do NOT pass `branch` here. If this work cycle is launched from the LFX workspace parent, the `target repo:` line is required so all three reviewers operate in this repo. The pinned SHAs are the immutable identity of the run: every report leads with the commit or range it reviewed, and the parent verifies it matches the pinned `target_sha`. A report for any other commit does not count — see step 5.
4. **Keep working.** Start the next commit while the reviewers run. Do not block on them.
5. **When the reviews return:** roll every Critical finding and every reasonable Important finding into the next commit. Reviewer children only report — the parent session makes every fix. A child that failed to load its assigned skill, whose report leads with `INCOMPLETE`, or whose report reviewed a commit other than the pinned `target_sha`, is not a pass: resolve the cause and relaunch the complete three-child batch on the same pinned range.

### Pre-PR (drain the queue, sweep cumulative state, then open)

When the work is done and no more code commits are planned:

1. **Wait for every running review to complete.**
2. **If any returned review flags Critical or reasonable Important:** add a fix commit, launch all three reviewers again on the new state, wait, and loop until clean or explicitly documented as a trade-off.
3. **Full-branch sweep — only if the branch has more than one commit.** Launch the same three skill-loading children again (one parallel batch, `subagent_type: general-purpose`, `model: opus`, `run_in_background: true`, one skill each as in the post-commit steps), with the shared inputs pinned for the branch:

   ```text
   target repo: lfx-v2-email-service
   branch
   target_sha: <git rev-parse HEAD>
   base_sha: <git merge-base origin/main HEAD>
   review exactly: <base_sha>...<target_sha>

   Review the branch's diff against origin/main.
   ```

   Address any new findings, then re-run all three sweeps until clean.
4. **Audit CLAUDE.md and docs/ for currency.** Run `git diff origin/main...HEAD --name-only` and compare every relevant section of `CLAUDE.md` and every file under `docs/` against the actual code in the branch. See **Docs currency checklist** below for the full lookup table. Commit any updates in the same PR — do not open a PR with stale documentation.
5. **Run `/email-service-pr-readiness`** for branch and PR-shape checks.
6. **Run `/email-service-preflight`** for mechanical Go validation and the PR change summary.
7. **Only then push and open the PR.** Use the standard PR title format:
   `<type>(<scope>): <summary> [<ticket>]`
   Types: `feat` | `fix` | `refactor` | `docs` | `chore`. Scope is optional but recommended. Ticket reference is optional — include `[LFXV2-XXXX]` when a ticket exists; omit the bracket entirely when there is no ticket. Do not use a placeholder like `[LFXV2-0000]`.

### Docs currency checklist

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

### Post-PR iteration (responding to bot feedback on an open PR)

1. Wait for Copilot (and `github-license-compliance[bot]`) to comment after each push.
2. Triage every Critical and reasonable Important finding against current code.
3. Roll fixes into a `fix(review): ...` commit.
4. Push. Repeat until clean.

### Keeping the PR title and description accurate

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
