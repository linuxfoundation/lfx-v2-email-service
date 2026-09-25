<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Email Service Contract

This document is the authoritative contract for the NATS request/reply surface owned by `lfx-v2-email-service`.

Update this document in the same PR as any change to `pkg/api/nats.go`, handler reply shapes, subject names, error strings, or tracking KV fields.

## Ownership

`lfx-v2-email-service` owns:

- Transactional email request/reply subjects.
- The public Go package `github.com/linuxfoundation/lfx-v2-email-service/pkg/api`.
- Email tracking records in the `email-recipients` NATS KV bucket.
- Group-to-email indexes in the `email-group-index` NATS KV bucket.
- Engagement analytics computed from those KV records.

The service does not render templates. Callers must send pre-rendered HTML and plain text.

## Subjects

### Request/Reply

| Constant | Subject | Reply |
| --- | --- | --- |
| `api.SendEmailSubject` | `lfx.email-service.send_email` | `SendEmailResponse` on success, `SendEmailErrorResponse` on failure |
| `api.GetEmailStatusSubject` | `lfx.email-service.get_email_status` | `EmailRecipientRecord` for `email_id`, `[]EmailRecipientRecord` for `group_id`, or `SendEmailErrorResponse` |
| `api.GetEmailEngagementAnalyticsSubject` | `lfx.email-service.get_email_engagement_analytics` | `GetEmailEngagementAnalyticsResponse` or `SendEmailErrorResponse` |

All request/reply subscriptions use queue group `api.QueueGroup`, value `lfx.email-service.queue`.

### Push (Publish-Only)

The service publishes engagement events as they arrive from SES so callers can react in real time without polling `get_email_status`.

| Constant | Subject | Payload |
| --- | --- | --- |
| `api.EmailDeliveredSubject` | `lfx.email-service.email_delivered` | `EmailDeliveredEvent` |
| `api.EmailOpenedSubject` | `lfx.email-service.email_opened` | `EmailOpenedEvent` |
| `api.EmailLinkClickedSubject` | `lfx.email-service.email_link_clicked` | `EmailLinkClickedEvent` |
| `api.EmailFailedSubject` | `lfx.email-service.email_failed` | `EmailFailedEvent` |

Push subjects are **best-effort**: a NATS publish failure is logged but does not affect the KV store update or the SQS message acknowledgement. Callers that require guaranteed delivery should poll `get_email_status` instead.

## Send Email

Subject: `lfx.email-service.send_email`

Request: `api.SendEmailRequest`

| Field | Required | Description |
| --- | --- | --- |
| `to` | yes | Recipient email address. |
| `subject` | yes | Email subject. |
| `html` | yes | Pre-rendered HTML body. |
| `text` | yes | Pre-rendered plain-text body. |
| `from` | no | Sender address override. The domain must be an exact match in `SMTP_ALLOWED_FROM_DOMAINS` (default: `lfx.linuxfoundation.org`). If omitted, the service default (`DEFAULT_SMTP_FROM`) is used. |
| `from_display_name` | no | Display name in the From header. If omitted, the service default (`DEFAULT_SMTP_FROM_DISPLAY_NAME`, default `"LFX Self Serve"`) is used. |
| `reply_to` | no | Sets the SMTP `Reply-To` header. The domain must be in `SMTP_ALLOWED_REPLY_TO_DOMAINS` (default: `linuxfoundation.org`); subdomain suffix matching applies, so the default also permits `lfx.linuxfoundation.org`. |
| `group_id` | no | Caller-supplied correlation ID for a batch or campaign. If omitted, the service generates one. |

Success reply: `api.SendEmailResponse`

| Field | Description |
| --- | --- |
| `email_id` | Service-generated UUID for this send. Used as the key in `email-recipients`. |
| `group_id` | Caller-provided or service-generated group ID. Used as the key in `email-group-index`. |

Error reply: `api.SendEmailErrorResponse`

| Error | Cause |
| --- | --- |
| `invalid request payload` | Request body is not valid JSON. |
| `to, subject, html, and text are required` | One or more required fields are empty. |
| `invalid from address` | `from` is set but is not a parseable email address. |
| `from address domain not allowed` | `from` domain is not in `SMTP_ALLOWED_FROM_DOMAINS`. |
| `invalid reply_to address` | `reply_to` is set but is not a parseable email address. |
| `reply_to address domain not allowed` | `reply_to` domain is not in `SMTP_ALLOWED_REPLY_TO_DOMAINS`. |
| `email delivery failed` | SMTP delivery failed after the service accepted the request. |

When `EMAIL_ENABLED=false`, the service uses `NoOpSender`: the request still succeeds but returns an empty `SendEmailResponse` (`email_id` and `group_id` both empty). No SMTP message is sent and no tracking records are written.

### Recipient domain filtering

When `SMTP_ALLOWED_RECIPIENT_DOMAINS` is non-empty (non-prod environments), a recipient whose
domain is not in the list (subdomain suffix matching applies) is **not** an error: the service
skips the send and replies with an empty `SendEmailResponse` (`email_id` and `group_id` both
empty), so callers do not treat expected non-prod filtering as a delivery failure. No tracking
records are written for skipped sends. When the variable is empty (production default), all
recipient domains are permitted.

## Get Email Status

Subject: `lfx.email-service.get_email_status`

Request: `api.GetEmailStatusRequest`

Exactly one field must be set:

| Field | Description |
| --- | --- |
| `email_id` | Fetch one `EmailRecipientRecord`. |
| `group_id` | Fetch all `EmailRecipientRecord` entries in a group. |

Reply:

- `email_id` lookup returns one `api.EmailRecipientRecord`.
- `group_id` lookup returns a JSON array of `api.EmailRecipientRecord`.
  The array may contain **fewer** entries than the group index lists: any per-recipient
  error (missing record, unmarshal failure, or transient KV read error) is silently
  omitted from the array rather than erroring, so the returned count can be less than
  the number of `email_id`s originally sent for the group. The response also includes
  `total_sent` (the raw index count) so callers can detect partial results.
- Error responses use `api.SendEmailErrorResponse`.

Error values:

| Error | Cause |
| --- | --- |
| `invalid request payload` | Request body is not valid JSON. |
| `email_id or group_id is required` | Neither lookup field was set. |
| `only one of email_id or group_id may be set` | Both lookup fields were set. |
| `not found` | No matching record or group index exists. |
| `internal error` | KV read, decode, or response serialization failed. |

## Engagement Analytics

Subject: `lfx.email-service.get_email_engagement_analytics`

Request: `api.GetEmailEngagementAnalyticsRequest`

| Field | Required | Description |
| --- | --- | --- |
| `group_id` | yes | Group ID returned by `send_email` or supplied by the caller. |

Success reply: `api.GetEmailEngagementAnalyticsResponse`

| Field | Description |
| --- | --- |
| `group_id` | Group ID queried. |
| `total_sent` | Count of email IDs in the group index. |
| `delivered` | Count of records with `delivered=true`. |
| `opened` | Total open count across records. |
| `unique_opened` | Count of records with at least one open. |
| `failed` | Count of records marked failed by bounce or complaint. |

Error values:

| Error | Cause |
| --- | --- |
| `invalid request payload` | Request body is not valid JSON. |
| `group_id is required` | The request omitted `group_id`. |
| `not found` | No group index exists for `group_id`. |
| `internal error` | Reading or decoding the group index failed. |

Only failures reading or decoding the **group index** return `internal error`. Per-recipient
`email-recipients` reads in the analytics loop are best-effort: a missing or corrupt recipient
record (any `KV.Get` error or unmarshal failure) is silently skipped and excluded from the
aggregate counts. `total_sent` reflects the number of `email_id`s in the group index, so the
sum of `delivered` / `failed` / `unique_opened` may be less than `total_sent` when records are
missing or unreadable.

## Engagement Push Events

Each push payload is a JSON-encoded struct from `pkg/api`.

### `EmailDeliveredEvent` (`api.EmailDeliveredSubject`)

| Field | Type | Description |
| --- | --- | --- |
| `email_id` | string | Per-send UUID. |
| `group_id` | string | Caller-supplied or service-generated group ID. |
| `delivered_at` | RFC3339 UTC | Timestamp from the SES DELIVERY event. |

### `EmailOpenedEvent` (`api.EmailOpenedSubject`)

| Field | Type | Description |
| --- | --- | --- |
| `email_id` | string | Per-send UUID. |
| `group_id` | string | Group ID. |
| `open_count` | int | Cumulative open count after this event. |
| `opened_at` | RFC3339 UTC | Timestamp of **this** SES OPEN event (not the max across all opens). |

Published once per unique SNS `MessageId`. A replayed SQS delivery of the same OPEN event produces no second publish.

### `EmailLinkClickedEvent` (`api.EmailLinkClickedSubject`)

| Field | Type | Description |
| --- | --- | --- |
| `email_id` | string | Per-send UUID. |
| `group_id` | string | Group ID. |
| `event_id` | string | SNS `MessageId` of this SES CLICK event. Use as the deduplication key for at-most-once processing (see note below). |
| `link` | string | URL that was clicked, with query string and fragment stripped to avoid exposing tokens or signed parameters. |
| `click_count` | int | Cumulative click count after this event. |
| `clicked_at` | RFC3339 UTC | Timestamp of **this** SES CLICK event. |

Published once per unique SNS `MessageId` **within the bounded deduplication window** (up to 500 unique click MessageIds per record, and subject to the KV record size limit). Once that window is exhausted, SQS replays of later clicks are not detected and will re-increment `click_count` and emit an additional push. Consumers that need guaranteed at-most-once processing should deduplicate on `event_id`. Note: `email_id` + `link` is **not** a valid deduplication key because a recipient may legitimately click the same link multiple times, each producing a distinct `event_id`.

### `EmailFailedEvent` (`api.EmailFailedSubject`)

| Field | Type | Description |
| --- | --- | --- |
| `email_id` | string | Per-send UUID. |
| `group_id` | string | Group ID. |
| `reason` | string | `"bounce"` or `"complaint"`. |
| `failed_at` | RFC3339 UTC | Timestamp from the SES BOUNCE or COMPLAINT event. |

Published at most once per email (BOUNCE and COMPLAINT are single-fire; subsequent events are ignored once `failed=true`).

## Tracking Record

`api.EmailRecipientRecord` is stored in `email-recipients`, keyed by `email_id`.

| Field | Description |
| --- | --- |
| `group_id` | Group/campaign correlation ID. |
| `email_id` | Per-send UUID. |
| `to` | Recipient email address. |
| `subject` | Email subject. |
| `sent_at` | UTC send timestamp. |
| `delivered`, `delivered_at` | Delivery event status and timestamp. |
| `opened`, `open_count`, `opened_at_list`, `last_opened_at` | Open event status, deduplicated event list (keyed by SNS `MessageId`), and aggregate count. |
| `clicked`, `click_count`, `click_event_ids`, `click_list`, `last_clicked_at` | Click event status, aggregate count, SNS `MessageId` dedup list (used for replay protection; bounded to 500 entries and by the KV record size limit; once exhausted, replays of later clicks are not detected), bounded click history (bounded by the KV record size limit; each entry includes `link` and `clicked_at`), and latest click timestamp. |
| `failed`, `failed_at` | Bounce or complaint status and timestamp. |

`email-group-index` stores a JSON `[]string` of `email_id` values, keyed by `group_id`.

## Change Checklist

- Update `pkg/api/nats.go`.
- Update handlers in `internal/service/`.
- Update this document.
- Update `docs/email-engagement-tracking.md` if the change touches tracking headers, KV fields, or SES events.
- Add or update table-driven tests.
- Run `make test` and `make check`.
