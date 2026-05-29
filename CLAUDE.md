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
>
> If the plugin is missing, install with `/plugin marketplace add linuxfoundation/lfx-skills` then `/plugin install lfx-skills@lfx-skills`.

## Service Overview

Thin NATS request/reply relay. Receives pre-rendered `{to, subject, html, text}`
payloads and delivers them via Amazon SES SMTP. No templates, no template registry —
callers are responsible for rendering their own content.

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
cmd/email-service/     → entry point, wiring, config
internal/domain/       → interfaces (Sender)
internal/service/      → NATS message handler
internal/infrastructure/smtp/ → SMTPSender, NoOpSender, MIME builder
internal/logging/      → structured log helpers
pkg/api/               → PUBLIC: NATS subject + wire types (callers import this)
pkg/redaction/         → email address redaction for logs
```

### Key design decisions

- **Pre-rendered only.** No template engine. Callers send HTML + plain text.
- **pkg/api is the public contract.** Any service that wants to send email imports
  `github.com/linuxfoundation/lfx-v2-email-service/pkg/api` for the subject constant
  and `SendEmailRequest` type. Never expose `internal/` packages to callers.
- **NoOpSender for local dev.** `EMAIL_ENABLED` defaults to `false` (NoOpSender logs
  instead of sending). Set `EMAIL_ENABLED=true` to enable real SMTP delivery.
- **Queue group for horizontal scaling.** The subscription uses queue group
  `lfx.email-service.queue` so each message is delivered to exactly one pod.
- **Handle always responds.** The NATS handler calls `msg.Respond` on every path
  (success → `nil`, failure → JSON error) so callers' `RequestWithContext` never
  hangs.

## Development Workflow

### Prerequisites

- Go 1.24+
- `nats` CLI (`brew install nats-io/nats-tools/nats`)
- Docker (for local NATS + Mailpit)

### Common tasks

```bash
make build          # compile binary to bin/email-service
make run            # build and run with env vars from shell
make test           # go test ./...
make test-coverage  # test with coverage report
make lint           # golangci-lint run
make fmt            # go fmt + gofmt -s (no goimports)
make check          # gofmt check + lint + license-check (does not run tests)
```

## Work cycle — post-commit and pre-PR reviews

> **CRITICAL — while the branch is pre-PR, post-commit review is mandatory.** After every commit on the local branch, launch both `lfx-skills:lfx-general-code-reviewer` and `lfx-skills:lfx-email-service-code-reviewer` subagents via the Agent tool with `run_in_background: true` — then keep working while they run. If Claude displays plugin agents without the `lfx-skills:` namespace, use the equivalent displayed general and email-service reviewer names. Before opening a PR, every running review must return clean (or remaining findings explicitly documented as trade-offs), the **full-branch sweep** must run clean if the branch has more than one commit (`branch` arg), AND `/email-service-pr-readiness` must clear every Critical finding before `/email-service-preflight` runs.
>
> **Once the PR is open, do NOT invoke these pre-PR reviewers on iteration commits.** CodeRabbit + Copilot auto-trigger on every push and own the audit surface from that point. The general and email-service reviewers are pre-PR insurance only.

### Post-commit (pre-PR phase, after every commit, asynchronous)

1. **Commit your work.** `git commit -s -S`. Do not wait for any prior review to finish.
2. **Immediately launch both reviewer subagents in parallel.** Use `subagent_type: lfx-skills:lfx-general-code-reviewer`, `run_in_background: true`, and `subagent_type: lfx-skills:lfx-email-service-code-reviewer`, `run_in_background: true`.
3. **Post-commit mode prompt for both reviewers (exact):** `target repo: lfx-v2-email-service\n\nReview the latest commit.` Append `extra: <focus>` on a new line only when there is a priority hint to add. Do NOT pass `branch` here. If this work cycle is launched from the LFX workspace parent, the `target repo:` line is required so both reviewers operate in this repo.
4. **Keep working.** Start the next commit while the reviewers run. Do not block on them.
5. **When the reviews return:** roll every Critical finding and every reasonable Important finding into the next commit.

### Pre-PR (drain the queue, sweep cumulative state, then open)

When the work is done and no more code commits are planned:

1. **Wait for every running review to complete.**
2. **If any returned review flags Critical or reasonable Important:** add a fix commit, launch both reviewers again on the new state, wait, and loop until clean or explicitly documented as a trade-off.
3. **Full-branch sweep — only if the branch has more than one commit.** Launch both `lfx-skills:lfx-general-code-reviewer` and `lfx-skills:lfx-email-service-code-reviewer` again with prompt **`target repo: lfx-v2-email-service\nbranch\n\nReview the branch's diff against origin/main.`**. Address any new findings, then re-run both sweeps until clean.
4. **Run `/email-service-pr-readiness`** for branch and PR-shape checks.
5. **Run `/email-service-preflight`** for mechanical Go validation and the PR change summary.
6. **Only then push and open the PR.**

### Post-PR iteration (responding to bot feedback on an open PR)

1. Wait for CodeRabbit + Copilot to comment after each push.
2. Triage every Critical and reasonable Important finding against current code.
3. Roll fixes into a `fix(review): ...` commit.
4. Push. Repeat until clean.

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
nats req lfx.email-service.send_email \
  '{"to":"test@example.com","subject":"Hello","html":"<p>Hi</p>","text":"Hi"}'
```

## NATS Subjects

| Constant | Value | Direction |
|---|---|---|
| `api.SendEmailSubject` | `lfx.email-service.send_email` | request/reply; reply is JSON `SendEmailResponse` |
| `api.QueueGroup` | `lfx.email-service.queue` | queue group for all subscriptions |
| `api.GetEmailStatusSubject` | `lfx.email-service.get_email_status` | request/reply; payload `GetEmailStatusRequest` → `EmailRecipientRecord` for `email_id`, `[]EmailRecipientRecord` for `group_id` |
| `api.GetEmailEngagementAnalyticsSubject` | `lfx.email-service.get_email_engagement_analytics` | request/reply; payload `GetEmailEngagementAnalyticsRequest` → `GetEmailEngagementAnalyticsResponse` |

All constants are in `pkg/api/nats.go`.

## NATS KV

| Constant | Bucket | Key | Value |
|---|---|---|---|
| `api.EmailRecipientsKVBucket` | `email-recipients` | `<email_id>` (UUID per send) | JSON `EmailRecipientRecord` |
| `api.EmailGroupIndexKVBucket` | `email-group-index` | `<group_id>` (UUID per campaign) | JSON `[]string` of `email_id`s |

The `email_id` and `group_id` are returned to callers in `SendEmailResponse`.
The `group_id` is optional in `SendEmailRequest` — if not provided the email service generates one.

## Environment Variables

| Variable | Default | Notes |
|---|---|---|
| `NATS_URL` | `nats://localhost:4222` | |
| `PORT` | `8080` | HTTP health probe port |
| `EMAIL_ENABLED` | `false` | `true`/`t`/`1` → SMTPSender; anything else → NoOpSender |
| `SMTP_HOST` | `localhost` | |
| `SMTP_PORT` | `587` | STARTTLS |
| `SMTP_FROM` | `noreply@lfx.linuxfoundation.org` | |
| `SMTP_USERNAME` | _(empty)_ | From K8s Secret in production |
| `SMTP_PASSWORD` | _(empty)_ | From K8s Secret in production |
| `SES_EVENTING_ENABLED` | `false` | `true`/`t`/`1` → start the SQS engagement event poller; fatal at startup if AWS config fails to load |
| `SES_CONFIGURATION_SET` | _(empty)_ | SES v2 configuration set name; when set adds `X-SES-CONFIGURATION-SET` header to outbound mail |
| `SES_ENGAGEMENT_SQS_QUEUE_URL` | _(empty)_ | SQS queue URL for SES engagement events; required when `SES_EVENTING_ENABLED=true` |
| `LOG_LEVEL` | `info` | |
| `LOG_ADD_SOURCE` | `false` | `true` → include file/line in log entries |

## Testing Patterns

- **Table-driven tests** in `_test.go` files co-located with source.
- **`mockSender`** in `internal/service/send_email_handler_test.go` — satisfies `domain.Sender`.
- **`HandleData`** on `SendEmailHandler` — testable entry point that takes raw bytes
  and a respond callback; `Handle` wraps it for real NATS messages. Use `HandleData`
  in tests instead of embedding a real NATS server.
- **Package `smtp` tests** use the unexported `buildEmailMessage` / `generateMessageID`
  / `generateBoundary` helpers directly (internal test package `package smtp`).

## Adding a New NATS Subject

1. Add the subject constant and any new wire types to `pkg/api/nats.go`.
2. Add a handler struct in `internal/service/` following the `SendEmailHandler` pattern.
3. Register the `QueueSubscribe` in `cmd/email-service/main.go`.
4. Add a table-driven test for `HandleData`.

## Code Conventions

- `slog.DebugContext` for success paths, `slog.WarnContext` for recoverable issues,
  `slog.ErrorContext` for unexpected failures.
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
