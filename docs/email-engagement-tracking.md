<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Email Engagement Tracking

This document owns the service-local behavior for SES engagement events, tracking headers, and NATS KV state.

Update it in the same PR as any change to SMTP tracking headers, SES/SQS handling, KV bucket fields, retry behavior, or chart values that enable engagement tracking.

## Flow

1. A caller sends a pre-rendered email request to `lfx.email-service.send_email`.
2. `SMTPSender` generates an `email_id` and resolves a `group_id`.
3. The MIME message includes `X-LFX-TRACKING-ID: <group_id>/<email_id>`.
4. If configured, the MIME message also includes `X-SES-CONFIGURATION-SET`.
5. After successful SMTP delivery, `SendEmailHandler` writes an `EmailRecipientRecord` to `email-recipients` and appends the `email_id` to `email-group-index`.
6. SES emits engagement events to SNS, SNS sends them to SQS, and the service's SQS poller consumes the queue.
7. `EngagementEventHandler` extracts `email_id`, loads the KV record, applies the event, and writes the updated record back with optimistic locking.
8. If the event was new (not a deduplicated replay), the handler publishes a typed push event to the appropriate NATS subject so subscribers can react in real time without polling.

## Tracking Headers

| Header | Source | Purpose |
| --- | --- | --- |
| `X-LFX-TRACKING-ID` | Always set by `SMTPSender` when sending through SMTP. | Correlates SES events back to the KV record. Format is `<group_id>/<email_id>`. |
| `X-SES-CONFIGURATION-SET` | Set only when `SES_CONFIGURATION_SET` is non-empty. | Routes SES engagement events to the configured SES event destination. |

The engagement handler extracts the email ID from the part after the last `/`, so group IDs may contain `/`.

## KV Buckets

| Bucket | Key | Value |
| --- | --- | --- |
| `email-recipients` | `<email_id>` | JSON `api.EmailRecipientRecord` |
| `email-group-index` | `<group_id>` | JSON `[]string` of `email_id` values |

Tracking is optional for basic sending. If JetStream or either bucket is missing, the service still subscribes to `send_email`, but status and analytics subjects are not registered.

When `SES_EVENTING_ENABLED=true`, `email-recipients` is required. Missing AWS config, missing queue URL, or missing recipient KV is fatal at startup.

## Event Handling

Supported SES event types:

| SES event | Record update | NATS push subject |
| --- | --- | --- |
| `DELIVERY` | Sets `delivered=true` and `delivered_at`. Single-fire; subsequent events are ignored. | `api.EmailDeliveredSubject` |
| `OPEN` | Appends to `opened_at_list`, sets `opened=true`, updates `open_count` and `last_opened_at`. Deduplicated by SNS `MessageId`. | `api.EmailOpenedSubject` |
| `CLICK` | Appends to `click_list` (includes link and timestamp), sets `clicked=true`, updates `click_count` and `last_clicked_at`. Deduplicated by SNS `MessageId`. | `api.EmailLinkClickedSubject` |
| `BOUNCE` | Sets `failed=true` and `failed_at`. Single-fire; subsequent events are ignored. | `api.EmailFailedSubject` (reason: `"bounce"`) |
| `COMPLAINT` | Sets `failed=true` and `failed_at`. Single-fire; subsequent events are ignored. | `api.EmailFailedSubject` (reason: `"complaint"`) |

OPEN and CLICK events are deduplicated by SNS `MessageId`. A replayed SQS delivery of the same
event ID is silently dropped: the KV record is not modified and no NATS push is emitted. This
prevents duplicate notifications when SQS redelivers a message after a transient visibility
timeout.

DELIVERY, BOUNCE, and COMPLAINT are single-fire: once the corresponding boolean (`delivered`,
`failed`) is set, subsequent events of the same type are ignored and not re-published.

Push events use the timestamp from the SES event itself, not an aggregate field such as
`last_opened_at`. This ensures the published timestamp is accurate even when older SES events
arrive out-of-order after a newer one.

Push publishing is best-effort: a NATS failure is logged but does not affect the KV write or the
SQS acknowledgement. See `docs/email-service-contract.md` for payload schemas.

Unknown event types, malformed SNS/SES payloads, and missing tracking headers are treated as non-retryable skips (handler returns `nil`, SQS message is deleted).

The engagement handler distinguishes two classes of `email-recipients` KV errors:

- **`ErrKeyNotFound`** — genuine miss (late-arriving SES event for an unknown email ID, or record already expired). Non-retryable: handler returns `nil`, SQS message is deleted.
- **Any other KV error** — transient read or network failure. Retryable: handler returns the error, SQS message is left on the queue and redelivered. This ensures engagement events are not silently dropped during a KV outage.

## SQS Poller

`internal/infrastructure/sqs.Poller`:

- Long-polls the configured queue with `WaitTimeSeconds=20`.
- Requests up to 10 messages per receive call.
- Deletes messages only after the handler succeeds.
- Leaves messages on the queue when the handler returns an error.
- Applies linear backoff after receive failures, capped at 30 seconds.
- Returns an error after three consecutive receive failures. The main process treats that as fatal and exits.

## Chart And AWS Ownership

The service chart owns the runtime wiring for:

- `SES_EVENTING_ENABLED`
- `SES_CONFIGURATION_SET`
- `SES_ENGAGEMENT_SQS_QUEUE_URL`
- External Secrets Operator resources that materialize those values into Kubernetes Secrets
- ServiceAccount annotations for IRSA

AWS-side SES configuration sets, SNS topics, SQS queues, IAM roles, and secret source values are owned outside this repo. Do not put secret values in this repo.

## Change Checklist

- Update `pkg/api/nats.go` if record fields or constants change.
- Update `internal/infrastructure/smtp/message.go` for header changes.
- Update `internal/service/engagement_event_handler.go` for event behavior changes.
- Update `charts/lfx-v2-email-service/` for runtime config changes.
- Update `docs/email-service-contract.md` if public replies or record fields change.
- Add unit tests for new event behavior.
