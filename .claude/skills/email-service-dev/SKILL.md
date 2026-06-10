---
name: email-service-dev
description: Repo-local Go coding conventions and implementation guidance for lfx-v2-email-service. Auto-attaches when editing Go code, NATS subjects and payloads, SMTP/SES/SQS tracking code, Makefile, Helm chart templates, or service-owned docs. Owns the email-service public NATS contract, tracking KV behavior, SMTP sender boundary, SES engagement poller, tests, formatting, linting, and license headers. Central platform composition stays in lfx-skills:lfx-platform-architecture; cross-repo routing stays in lfx-skills:lfx.
paths:
  - "**/*.go"
  - "go.mod"
  - "go.sum"
  - "Makefile"
  - "cmd/**"
  - "internal/**"
  - "pkg/**"
  - "charts/**"
  - "docs/**"
  - ".claude/skills/email-service-dev/**"
allowed-tools: Read, Glob, Grep, Edit, Write, Bash
---

# Development Conventions

Repo-owned conventions for `lfx-v2-email-service`. This service is a thin NATS request/reply email relay with optional NATS KV engagement tracking. It is not a Goa resource service, does not own an OpenFGA type, and does not publish indexer or FGA messages.

Use this skill alongside:

- `lfx-skills:lfx` for cross-repo topology, owner lookup, and missing local checkouts.
- `lfx-skills:lfx-platform-architecture` for platform service classes, NATS/KV placement, Helm/ArgoCD handoffs, and cross-service flow.
- `docs/email-service-contract.md` for the public NATS subject and payload contract.
- `docs/email-engagement-tracking.md` for SES, SQS, tracking headers, and KV record behavior.
- `docs/service-helm-chart.md` for service-local chart values, secrets, and deployment surfaces.

## Repo Layout

```text
cmd/email-service/                  entry point, env parsing, NATS subscriptions, health probes, shutdown
internal/domain/                    Sender interface
internal/infrastructure/smtp/       SMTP/SES sender, MIME builder, NoOpSender
internal/infrastructure/sqs/        SQS long-polling engagement-event consumer
internal/logging/                   slog context helper and global logger setup
internal/service/                   NATS handlers and SES engagement event handler
internal/service/mocks/             NATS KV fake for unit tests
pkg/api/                            public NATS subjects, payloads, KV bucket constants
pkg/redaction/                      email redaction helpers for logs
charts/lfx-v2-email-service/        service-local Helm chart
docs/                               service-owned public contract and deployment docs
```

Keep implementation details in `internal/`. Anything callers import belongs in `pkg/api/`.

## Public Contract

- `pkg/api/nats.go` is the public Go contract for other services. Any subject, payload, response, or KV bucket change must update `docs/email-service-contract.md` in the same PR.
- Do not hardcode subject strings at call sites. Use `api.SendEmailSubject`, `api.GetEmailStatusSubject`, `api.GetEmailEngagementAnalyticsSubject`, and `api.QueueGroup`.
- Callers send pre-rendered HTML and text. Do not add a template registry or rendering engine here unless the product architecture changes.
- `SendEmailHandler.HandleData` must call the response callback exactly once on every path. NATS callers use request/reply and must not hang.
- Group status lookups return `[]api.EmailRecipientRecord`; single-email status returns one `api.EmailRecipientRecord`.

## NATS And KV

- The service subscribes with queue group `lfx.email-service.queue` so replicas share work.
- `email-recipients` stores one `api.EmailRecipientRecord` per `email_id`.
- `email-group-index` maps a `group_id` to `[]string` of `email_id` values.
- Tracking is optional. If JetStream or either KV bucket is missing, send requests still work, but status and analytics subjects are not subscribed.
- KV writes use optimistic locking where concurrent updates are possible. Preserve the retry behavior in group-index and engagement-event updates.
- Never write to another service's KV bucket. This service owns only the two email tracking buckets above.

## SMTP And SES

- The service accepts already-rendered content and sends via `net/smtp`.
- `EMAIL_ENABLED=false` selects `NoOpSender`, which returns successful IDs without sending SMTP mail.
- `SMTPSender.Send` creates both `email_id` and `group_id`, builds a MIME multipart message, and applies a 30-second timeout around the blocking SMTP call.
- Domain allowlist validation happens in `SendEmailHandler`, not in the sender: per-message `from` requires an exact-match domain in `SMTP_ALLOWED_FROM_DOMAINS`; `reply_to` requires a suffix-match domain in `SMTP_ALLOWED_REPLY_TO_DOMAINS`. Disallowed values return documented error replies.
- Recipient filtering (`SMTP_ALLOWED_RECIPIENT_DOMAINS`, empty = permit all) also happens in the handler. A blocked recipient is not an error: the handler replies with an empty `SendEmailResponse` and writes no tracking records. Preserve this empty-success semantic.
- The MIME builder strips CR/LF from header values. Preserve this when changing headers.
- `SES_CONFIGURATION_SET`, when non-empty, adds `X-SES-CONFIGURATION-SET`.
- `X-LFX-TRACKING-ID` is `group_id/email_id`. The SQS engagement handler extracts the part after the last `/`.

## SES Engagement Tracking

- `SES_EVENTING_ENABLED=true` starts the SQS poller and makes missing `SES_ENGAGEMENT_SQS_QUEUE_URL`, AWS config, or `email-recipients` KV a fatal startup error.
- The poller long-polls up to 10 SQS messages with a 20-second wait, deletes only successfully handled messages, and aborts after three consecutive receive failures.
- The handler processes SNS-wrapped SES `OPEN`, `DELIVERY`, `BOUNCE`, and `COMPLAINT` events. Unknown events and missing records are ignored.
- Open events are deduplicated by SNS `MessageId`; delivery, bounce, and complaint are idempotent booleans.
- Update `docs/email-engagement-tracking.md` with any change to headers, event handling, KV fields, retry behavior, or required AWS resources.

## Logging

- Use `log/slog` with `slog.*Context` variants.
- Add ambient fields with `logging.AppendCtx(ctx, slog.String(...))` when a field should appear through a handler call.
- Use `logging.ErrKey` for error fields.
- Redact email addresses in log fields with `redaction.RedactEmail`.
- Never log SMTP credentials, AWS secrets, raw payloads containing message bodies, raw SQS body content, or full recipient email addresses.

## Errors

- NATS error replies use JSON `api.SendEmailErrorResponse` with stable short strings. Keep these documented in `docs/email-service-contract.md`.
- Return caller-safe errors at the NATS boundary. Log internal details server-side.
- Wrap internal errors with `%w` where they cross function boundaries so tests and callers can classify root causes.

## Tests

- Co-locate `*_test.go` with the code under test.
- Use table-driven tests for branching behavior.
- Use `SendEmailHandler.HandleData` and the handler-level `HandleData` helpers instead of standing up a real NATS server in unit tests.
- Use `internal/service/mocks.NewKeyValue()` for KV behavior.
- Same-package tests are acceptable when exercising unexported SMTP helpers such as `buildEmailMessage`.
- Run `make test` before handoff; it enables the race detector.

## Formatting, Linting, License

- `make fmt` runs `go fmt ./...` and `gofmt -s`.
- `make lint` requires `golangci-lint`; `make check` runs format check, lint, and license check.
- Every new `.go` file starts with:

  ```go
  // Copyright The Linux Foundation and each contributor to LFX.
  // SPDX-License-Identifier: MIT
  ```

- Every new `.md` file in this repo starts with the HTML-comment license header.
- Document exported Go symbols when the linter requires it. Add implementation comments only when they clarify non-obvious behavior.

## References

- `references/go-conventions.md`: package boundaries, handler shape, logging, testing, and review checklist for this repo.

## Chart Work

- Service-local chart truth lives under `charts/lfx-v2-email-service/` and `docs/service-helm-chart.md`.
- Shared chart conventions live in `lfx-v2-helm/docs/service-chart-patterns.md`.
- Deployed environment values, image tags, IRSA role annotations, and environment promotion live in `lfx-v2-argocd`.
- SMTP credentials and SES engagement config are delivered through Kubernetes Secrets, optionally created by External Secrets Operator. Do not put secret values in docs, chart defaults, or logs.

## Boundaries

- This repo owns transactional email delivery and engagement tracking. It does not render email templates.
- This repo does not own newsletter composition or newsletter persistence; those live in `lfx-v2-newsletter-service`.
- This repo does not own Auth0, OpenFGA, query-service, indexer-service, platform NATS deployment, ArgoCD values, or AWS-side SES/SNS/SQS provisioning.
- If a change requires a peer repo, use `lfx-skills:lfx` to locate or clone it and read that repo's `CLAUDE.md` plus contract docs.
