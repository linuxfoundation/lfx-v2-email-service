# CLAUDE.md — lfx-v2-email-service

Development guide for Claude instances working on this service.

## Service Overview

Thin NATS request/reply relay. Receives pre-rendered `{to, subject, html, text}`
payloads and delivers them via Amazon SES SMTP. No templates, no template registry —
callers are responsible for rendering their own content.

**Technologies:** Go 1.24, NATS (`nats.go`), `net/smtp`, Kubernetes/Helm

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

### Local dev loop

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

## NATS Subject

| Constant | Value |
|---|---|
| `api.SendEmailSubject` | `lfx.email-service.send_email` |
| `api.QueueGroup` | `lfx.email-service.queue` |

Both are in `pkg/api/nats.go`.

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
