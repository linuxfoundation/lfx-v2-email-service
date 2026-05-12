# LFX V2 Email Service

Thin transactional email relay for the LFX Self-Service platform. Receives
pre-rendered email payloads over NATS request/reply and delivers them via
Amazon SES SMTP.

## NATS Contract

| Subject | Direction | Description |
|---|---|---|
| `lfx.email-service.send_email` | inbound request/reply | Send a pre-rendered email |

### Send Email

**Subject:** `lfx.email-service.send_email`  
**Queue group:** `lfx.email-service.queue`

**Request payload:**
```json
{
  "to": "user@example.com",
  "subject": "You've been added as a Writer on Demo Project",
  "html": "<html>...</html>",
  "text": "You've been added as a Writer on Demo Project."
}
```

**Success response:** empty body (`nil`)

**Error response:**
```json
{ "error": "<reason>" }
```

## Quick Start

### Prerequisites

- [NATS Server](https://docs.nats.io/running-a-nats-service/introduction/installation) or Docker
- [Mailpit](https://github.com/axllent/mailpit) for local SMTP capture (optional)

### Run locally

```bash
# Start NATS
docker run -d -p 4222:4222 nats:latest

# Start Mailpit (local SMTP capture, UI at localhost:8025)
docker run -d -p 1025:1025 -p 8025:8025 axllent/mailpit

# Run the service
SMTP_HOST=localhost SMTP_PORT=1025 NATS_URL=nats://localhost:4222 make run
```

### Send a test email

```bash
nats req lfx.email-service.send_email \
  '{"to":"alice@example.com","subject":"Test","html":"<p>Hi</p>","text":"Hi"}'
```

### Helm install (local cluster)

```bash
make helm-install-local
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `NATS_URL` | `nats://localhost:4222` | NATS server URL |
| `PORT` | `8080` | HTTP health probe port |
| `EMAIL_ENABLED` | `true` | Set `false` to log instead of sending (NoOpSender) |
| `SMTP_HOST` | `localhost` | SMTP server hostname |
| `SMTP_PORT` | `587` | SMTP server port (STARTTLS) |
| `SMTP_FROM` | `noreply@lfx.linuxfoundation.org` | Envelope From address |
| `SMTP_USERNAME` | _(empty)_ | SMTP credential (from Kubernetes Secret in production) |
| `SMTP_PASSWORD` | _(empty)_ | SMTP credential (from Kubernetes Secret in production) |
| `LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `LOG_ADD_SOURCE` | `false` | Set `true` to include source file/line in log entries |

## File Structure

```
lfx-v2-email-service/
├── cmd/email-service/
│   ├── main.go          # Entry point: NATS subscription, HTTP health, graceful shutdown
│   └── config.go        # Environment variable parsing
├── internal/
│   ├── domain/
│   │   └── email.go     # Sender interface
│   ├── infrastructure/
│   │   └── smtp/
│   │       ├── sender.go    # SMTPSender — delivers via net/smtp
│   │       ├── noop.go      # NoOpSender — logs only (EMAIL_ENABLED=false)
│   │       └── message.go   # MIME message builder
│   ├── logging/
│   │   └── logging.go   # Structured log helpers
│   └── service/
│       └── handler.go   # NATS message handler
├── pkg/
│   ├── api/
│   │   └── nats.go      # Public NATS subject + request/response types (import this)
│   └── redaction/
│       └── redaction.go # Email address redaction for logs
└── charts/lfx-v2-email-service/
    ├── Chart.yaml
    ├── values.yaml
    └── templates/
        ├── deployment.yaml
        └── service.yaml
```

## Calling from Another Service

Import `pkg/api` to get the subject constant and wire types:

```go
import emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"

req := emailapi.SendEmailRequest{
    To:      "user@example.com",
    Subject: "You've been added",
    HTML:    html,
    Text:    plain,
}
data, _ := json.Marshal(req)
reply, err := nc.RequestWithContext(ctx, emailapi.SendEmailSubject, data)
```

## Development

All commits must be signed off per the [DCO](https://developercertificate.org/):

```bash
git commit -s -m "feat: ..."
```

## License

Copyright The Linux Foundation and each contributor to LFX.
SPDX-License-Identifier: MIT
