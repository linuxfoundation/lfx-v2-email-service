<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Go Conventions

Use this reference when editing implementation code in `lfx-v2-email-service`.

## Package Boundaries

| Area | Rule |
| --- | --- |
| `cmd/email-service/` | Startup wiring, env parsing, NATS subscriptions, HTTP health probes, graceful shutdown. |
| `pkg/api/` | Public NATS subjects, payloads, response types, and KV bucket constants. Safe for callers to import. |
| `internal/service/` | NATS handlers and SES engagement event handling. |
| `internal/infrastructure/smtp/` | SMTP sender and MIME construction. |
| `internal/infrastructure/sqs/` | SQS long-polling consumer. |
| `internal/domain/` | Interfaces used by service handlers. |

Do not move public payloads into `internal/`, and do not expose implementation packages to callers.

## Handler Shape

- NATS handlers must reply exactly once on every request path.
- Keep a testable core method that accepts raw bytes and a response callback where practical.
- Use queue subscriptions with `api.QueueGroup`.
- Keep subject names and bucket names in `pkg/api`.
- Log internal errors, but return caller-safe JSON error strings.

## Logging

- Use `slog.*Context`.
- Use `logging.ErrKey` for errors.
- Use `logging.AppendCtx` for fields reused across a call.
- Redact email addresses with `redaction.RedactEmail`.
- Do not log message bodies, SMTP credentials, SQS bodies, or full recipient addresses.

## Testing

- Prefer table-driven tests.
- Use `internal/service/mocks.NewKeyValue()` for KV behavior.
- Test SMTP MIME details in same-package tests when unexported helpers are involved.
- Run `make test` after implementation changes.

## Review Checklist

- New or changed public contract? Update `docs/email-service-contract.md`.
- New or changed SES/KV tracking behavior? Update `docs/email-engagement-tracking.md`.
- New or changed chart value? Update `docs/service-helm-chart.md`.
- New Go files have the standard two-line license header.
