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

| Constant | Subject | Reply |
| --- | --- | --- |
| `api.SendEmailSubject` | `lfx.email-service.send_email` | `SendEmailResponse` on success, `SendEmailErrorResponse` on failure |
| `api.GetEmailStatusSubject` | `lfx.email-service.get_email_status` | `EmailRecipientRecord` for `email_id`, `[]EmailRecipientRecord` for `group_id`, or `SendEmailErrorResponse` |
| `api.GetEmailEngagementAnalyticsSubject` | `lfx.email-service.get_email_engagement_analytics` | `GetEmailEngagementAnalyticsResponse` or `SendEmailErrorResponse` |

All subscriptions use queue group `api.QueueGroup`, value `lfx.email-service.queue`.

## Send Email

Subject: `lfx.email-service.send_email`

Request: `api.SendEmailRequest`

| Field | Required | Description |
| --- | --- | --- |
| `to` | yes | Recipient email address. |
| `subject` | yes | Email subject. |
| `html` | yes | Pre-rendered HTML body. |
| `text` | yes | Pre-rendered plain-text body. |
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
| `email delivery failed` | SMTP delivery failed after the service accepted the request. |

When `EMAIL_ENABLED=false`, the service uses `NoOpSender`: the request still succeeds and returns generated IDs, but no SMTP message is sent.

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
| `internal error` | KV read or decode failed. |

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
| `opened`, `open_count`, `opened_at_list`, `last_opened_at` | Open event status, deduplicated event list, and aggregate count. |
| `failed`, `failed_at` | Bounce or complaint status and timestamp. |

`email-group-index` stores a JSON `[]string` of `email_id` values, keyed by `group_id`.

## Change Checklist

- Update `pkg/api/nats.go`.
- Update handlers in `internal/service/`.
- Update this document.
- Update `docs/email-engagement-tracking.md` if the change touches tracking headers, KV fields, or SES events.
- Add or update table-driven tests.
- Run `make test` and `make check`.
