# LFX V2 Email Service

Thin transactional email relay for the LFX Self-Service platform. Receives
pre-rendered email payloads over NATS request/reply and delivers them via
Amazon SES SMTP.

## Usage

### Send via NATS

**Subject:** `lfx.email-service.send_email`  
**Queue group:** `lfx.email-service.queue`

**Request payload fields:**

| Field | Type | Description |
|---|---|---|
| `to` | string | Recipient email address |
| `subject` | string | Email subject line |
| `html` | string | HTML body — callers render this before publishing |
| `text` | string | Plain-text body — shown by clients that don't render HTML |

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

**Example (NATS CLI):**
```bash
nats req lfx.email-service.send_email \
  '{"to":"alice@example.com","subject":"Test","html":"<p>Hi</p>","text":"Hi"}'
```

### Send from Go

The `pkg/api` package exports the subject constant and request/response types,
so callers don't have to hardcode the wire format.

Add it to your module:

```bash
go get github.com/linuxfoundation/lfx-v2-email-service/pkg/api
```

Then publish a request over an existing NATS connection:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	emailapi "github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

func main() {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		panic(err)
	}
	defer nc.Drain()

	req := emailapi.SendEmailRequest{
		To:      "user@example.com",
		Subject: "You've been added",
		HTML:    "<p>Hello</p>",
		Text:    "Hello",
	}
	data, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reply, err := nc.RequestWithContext(ctx, emailapi.SendEmailSubject, data)
	if err != nil {
		panic(err) // transport / timeout error
	}

	if len(reply.Data) == 0 {
		fmt.Println("email accepted")
		return
	}

	var errResp emailapi.SendEmailErrorResponse
	if err := json.Unmarshal(reply.Data, &errResp); err != nil {
		panic(err)
	}
	fmt.Println("send failed:", errResp.Error)
}
```

## Quick Start

### Prerequisites

- Go 1.24+
- [NATS Server](https://docs.nats.io/running-a-nats-service/introduction/installation) or Docker
- Local Kubernetes cluster with [OrbStack](https://orbstack.dev/) or similar
- Mailpit running in the cluster for local SMTP capture (UI at `http://localhost:8025`)

### Option 1 — Run directly with `make run`

This runs the service as a local process, connecting to NATS and Mailpit in your cluster.

```bash
# 1. Copy the example env file and adjust as needed
cp .env.example .env

# 2. Source the env vars and run the service
source .env && make run
```

`.env` is gitignored and never committed. `SMTP_USERNAME` and `SMTP_PASSWORD` can be
left empty when pointing at Mailpit (no auth required).

### Option 2 — Build and deploy to local cluster with Helm

This builds a Docker image and installs the service into your local Kubernetes cluster.

```bash
# 1. Copy the example Helm values and adjust as needed
cp charts/lfx-v2-email-service/values.local.example.yaml \
   charts/lfx-v2-email-service/values.local.yaml

# 2. Build the image and install
make docker-build
make helm-install-local
```

`values.local.yaml` is gitignored. The example file is pre-configured to use Mailpit
(`lfx-platform-mailpit-smtp.lfx.svc.cluster.local:25`) with no SMTP credentials required.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `NATS_URL` | `nats://localhost:4222` | NATS server URL |
| `PORT` | `8080` | HTTP health probe port |
| `EMAIL_ENABLED` | `false` | Set `true` to enable SMTP delivery; unset/empty uses NoOpSender |
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
│       └── send_email_handler.go   # NATS message handler
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

## Development

All commits must be signed off per the [DCO](https://developercertificate.org/):

```bash
git commit -s -m "feat: ..."
```

## License

Copyright The Linux Foundation and each contributor to LFX.
SPDX-License-Identifier: MIT
