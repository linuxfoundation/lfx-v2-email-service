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
| `api.GetEmailStatusSubject` | `lfx.email-service.get_email_status` | `EmailRecipientRecord` for `group_id` + `email_id`, `[]EmailRecipientRecord` for `group_id` alone, or `SendEmailErrorResponse` |
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
| `group_id` | no | Group handle for a batch or campaign. Omit it to start a new group: the service issues a handle and returns it in `SendEmailResponse.group_id`. To add a send to an existing group, pass a handle previously returned by `send_email`. Any other value (wrong format, or a well-formed handle with no group index entry) is rejected before sending, except in degraded mode (see [Group Handles](#group-handles)). |

Field lengths are checked, in bytes, before any address is parsed or any field reaches a header
or the SMTP envelope: `to`, `from`, and `reply_to` are limited to `api.MaxAddressFieldLength`
(512) including any display name, with the address itself limited to `api.MaxAddressLength`
(254) and its local part to `api.MaxAddressLocalPartLength` (64), per RFC 5321; `subject` to
`api.MaxSubjectLength` (998); `from_display_name` to `api.MaxFromDisplayNameLength` (256).
An oversized field is rejected with the matching `... too long` error below.

Success reply: `api.SendEmailResponse`

| Field | Description |
| --- | --- |
| `email_id` | Service-generated UUID for this send. Used as the key in `email-recipients`. |
| `group_id` | Service-issued group handle (`grp_` + 32 lowercase hex chars). Used as the key in `email-group-index`. |

Error reply: `api.SendEmailErrorResponse`

| Error | Cause |
| --- | --- |
| `invalid request payload` | Request body is not valid JSON. |
| `to, subject, html, and text are required` | One or more required fields are empty. |
| `to address too long` | `to` exceeds `api.MaxAddressFieldLength` (512) bytes, or its address exceeds `api.MaxAddressLength` (254) bytes or its local part `api.MaxAddressLocalPartLength` (64) bytes (RFC 5321). No mail is sent. |
| `from address too long` | `from` exceeds the same limits as `to`. No mail is sent. |
| `reply_to address too long` | `reply_to` exceeds the same limits as `to`. No mail is sent. |
| `subject too long` | `subject` exceeds `api.MaxSubjectLength` (998) bytes. No mail is sent. |
| `from_display_name too long` | `from_display_name` exceeds `api.MaxFromDisplayNameLength` (256) bytes. No mail is sent. |
| `invalid from address` | `from` is set but is not a parseable email address, contains non-ASCII characters, or its local part would require RFC 5322 quoting (only ASCII dot-atom local parts are accepted). |
| `from address domain not allowed` | `from` domain is not in `SMTP_ALLOWED_FROM_DOMAINS`. |
| `invalid reply_to address` | `reply_to` is set but is not a parseable email address, contains non-ASCII characters, or its local part would require RFC 5322 quoting (only ASCII dot-atom local parts are accepted). |
| `reply_to address domain not allowed` | `reply_to` domain is not in `SMTP_ALLOWED_REPLY_TO_DOMAINS`. |
| `invalid group_id` | `group_id` is not a group handle issued by this service: wrong format, or no group index entry exists for it. |
| `group is full` | The group already holds `api.MaxGroupEmails` (10,000) emails. No mail is sent; start a new group by omitting `group_id`. |
| `internal error` | The group index could not be read to verify `group_id`. No mail is sent. |
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

| Field | Required | Description |
| --- | --- | --- |
| `group_id` | yes | Group handle returned by `send_email`. Alone, fetches one page of the group's `EmailRecipientRecord` entries. |
| `email_id` | no | With `group_id`, fetches one `EmailRecipientRecord`. The record is returned only if it was sent under `group_id`; otherwise the reply is `not found`. |
| `offset` | no | Group lookups only: index position of the first entry in the page. Default `0`; must not be negative. |
| `limit` | no | Group lookups only: number of index entries in the page. Default `api.DefaultGroupStatusLimit` (500); at most `api.MaxGroupStatusLimit` (1,000). |

Reply:

- `group_id` + `email_id` lookup returns one `api.EmailRecipientRecord`.
- `group_id` lookup returns a JSON array of `api.EmailRecipientRecord` for the group index
  entries at positions `[offset, offset+limit)`, in index order. To read a whole group, request
  successive pages (`offset += limit`) until `offset` reaches `total_sent` from
  `get_email_engagement_analytics`; an `offset` at or past the end returns `[]`.
  The array may contain **fewer** entries than the group index lists: any per-recipient
  error (missing record, unmarshal failure, or transient KV read error) is silently
  omitted from the array rather than erroring (as is any record whose `group_id` does not match the requested group), so the returned count can be less than
  the number of `email_id`s originally sent for the group. Index entries that are not
  valid UUIDs are also omitted, without any recipient KV read. The group-status reply
  carries no total count; callers that need the raw index count to detect partial
  results should use `get_email_engagement_analytics` (`total_sent`).
- Error responses use `api.SendEmailErrorResponse`.

Error values:

| Error | Cause |
| --- | --- |
| `invalid request payload` | Request body is not valid JSON. |
| `group_id is required` | `group_id` was not set (including an `email_id`-only request). |
| `invalid email_id` | `email_id` is not a valid UUID (8-4-4-4-12 hex, case-insensitive; normalized to lowercase before lookup). |
| `invalid group_id` | `group_id` is not in the group handle format issued by this service. |
| `invalid offset` | `offset` is negative. |
| `invalid limit` | `limit` is negative or greater than `api.MaxGroupStatusLimit`. |
| `not found` | No group index exists for `group_id`, or the `email_id` record does not exist or was not sent under `group_id`. |
| `response too large` | The requested page would exceed the NATS connection's max payload. The whole page is refused; request it again with a smaller `limit`. |
| `timeout` | Resolving the page took longer than the per-request deadline (5 seconds). |
| `service busy` | The replica is already handling its maximum number of concurrent status and analytics requests (8). Retry with backoff. |
| `internal error` | KV read, decode, or response serialization failed. |

## Engagement Analytics

Subject: `lfx.email-service.get_email_engagement_analytics`

Request: `api.GetEmailEngagementAnalyticsRequest`

| Field | Required | Description |
| --- | --- | --- |
| `group_id` | yes | Group handle returned by `send_email`. |

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
| `invalid group_id` | `group_id` is not in the group handle format issued by this service. |
| `not found` | No group index exists for `group_id`. |
| `timeout` | Resolving the group took longer than the per-request deadline (10 seconds). |
| `service busy` | The replica is already handling its maximum number of concurrent status and analytics requests (8). Retry with backoff. |
| `internal error` | Reading or decoding the group index failed. |

Only failures reading or decoding the **group index** return `internal error`. Per-recipient
`email-recipients` reads in the analytics loop are best-effort: a missing or corrupt recipient
record (any `KV.Get` error or unmarshal failure) is silently skipped and excluded from the
aggregate counts, as is any record whose `group_id` does not match the requested group.
Group-index entries that are not valid UUIDs are skipped the same way, without any recipient
KV read. `total_sent` reflects the number of `email_id`s in the group index, so the
sum of `delivered` / `failed` / `unique_opened` may be less than `total_sent` when records are
missing or unreadable. At most the first `api.MaxGroupEmails` index entries are resolved; only
an index written before the cap existed can hold more.

## Engagement Push Events

Each push payload is a JSON-encoded struct from `pkg/api`.

Push events do not carry the group handle (the `GroupID` struct field is kept for compile compatibility but is always empty). Push subjects can be read by any permitted NATS subscriber, and the group handle is the credential for a group's tracking records. Correlate events using the `email_id` returned by `send_email`.

### `EmailDeliveredEvent` (`api.EmailDeliveredSubject`)

| Field | Type | Description |
| --- | --- | --- |
| `email_id` | string | Per-send UUID. |
| `group_id` | string | Deprecated; always omitted. See note below. |
| `delivered_at` | RFC3339 UTC | Timestamp from the SES DELIVERY event. |

### `EmailOpenedEvent` (`api.EmailOpenedSubject`)

| Field | Type | Description |
| --- | --- | --- |
| `email_id` | string | Per-send UUID. |
| `group_id` | string | Deprecated; always omitted. |
| `open_count` | int | Cumulative open count after this event. |
| `opened_at` | RFC3339 UTC | Timestamp of **this** SES OPEN event (not the max across all opens). |

Published once per unique SNS `MessageId` **within the bounded deduplication window** (up to 500 unique open MessageIds per record, and subject to the KV record size limit). Once that window is exhausted, SQS replays of later opens are not detected and will re-increment `open_count` and emit an additional push.

### `EmailLinkClickedEvent` (`api.EmailLinkClickedSubject`)

| Field | Type | Description |
| --- | --- | --- |
| `email_id` | string | Per-send UUID. |
| `group_id` | string | Deprecated; always omitted. |
| `event_id` | string | SNS `MessageId` of this SES CLICK event. Use as the deduplication key for at-most-once processing (see note below). |
| `link` | string | URL that was clicked, with query string and fragment stripped to avoid exposing tokens or signed parameters. |
| `click_count` | int | Cumulative click count after this event. |
| `clicked_at` | RFC3339 UTC | Timestamp of **this** SES CLICK event. |

Published once per unique SNS `MessageId` **within the bounded deduplication window** (up to 500 unique click MessageIds per record, and subject to the KV record size limit). Once that window is exhausted, SQS replays of later clicks are not detected and will re-increment `click_count` and emit an additional push. Consumers that need guaranteed at-most-once processing should deduplicate on `event_id`. Note: `email_id` + `link` is **not** a valid deduplication key because a recipient may legitimately click the same link multiple times, each producing a distinct `event_id`.

### `EmailFailedEvent` (`api.EmailFailedSubject`)

| Field | Type | Description |
| --- | --- | --- |
| `email_id` | string | Per-send UUID. |
| `group_id` | string | Deprecated; always omitted. |
| `reason` | string | `"bounce"` or `"complaint"`. |
| `failed_at` | RFC3339 UTC | Timestamp from the SES BOUNCE or COMPLAINT event. |

Published at most once per email (BOUNCE and COMPLAINT are single-fire; subsequent events are ignored once `failed=true`).

## Tracking Record

`api.EmailRecipientRecord` is stored in `email-recipients`, keyed by `email_id`.

| Field | Description |
| --- | --- |
| `group_id` | Group handle the email was sent under. |
| `email_id` | Per-send UUID. |
| `to` | Recipient email address. |
| `subject` | Email subject. |
| `sent_at` | UTC send timestamp. |
| `delivered`, `delivered_at` | Delivery event status and timestamp. |
| `opened`, `open_count`, `opened_at_list`, `last_opened_at` | Open event status, aggregate count, open history doubling as the SNS `MessageId` dedup list (bounded to 500 entries and by the KV record size limit; once exhausted, opens are still counted but replays of later opens are not detected), and latest open timestamp. |
| `clicked`, `click_count`, `click_event_ids`, `click_list`, `last_clicked_at` | Click event status, aggregate count, SNS `MessageId` dedup list (used for replay protection; bounded to 500 entries and by the KV record size limit; once exhausted, replays of later clicks are not detected), bounded click history (bounded by the KV record size limit; each entry includes `link` and `clicked_at`), and latest click timestamp. |
| `failed`, `failed_at` | Bounce or complaint status and timestamp. |

`email-group-index` stores a JSON `[]string` of `email_id` values, keyed by `group_id`.

## Group Size and Read Bounds

Group reads are bounded so that one small request cannot cause work, memory, or a reply
proportional to an arbitrarily large group:

- A group holds at most `api.MaxGroupEmails` (10,000) emails, which keeps a group index value
  (about 39 bytes per entry) far below the bucket's 1 MiB `maxValueSize`. `send_email` to a full
  group is rejected with `group is full` before any mail is sent. If concurrent sends fill the
  group after that check, the email is still sent, but the reply carries an empty `group_id` and
  no recipient record is written, as for a new group that could not be recorded.
- Group status replies are paged (`offset` / `limit`), and a page that would exceed the
  connection's max payload is answered with `response too large` instead of being dropped.
- Status and analytics requests resolve recipient records 16 at a time under a per-request
  deadline (`timeout` when exceeded): 5 seconds for a status page, 10 seconds for analytics. The
  deadline is checked before and after each batch of reads, so a request can run past it by at
  most one KV read before it replies `timeout`. Analytics aggregates while reading and never holds the group's records in memory.
- Each replica runs at most 8 status and analytics requests at once, off the NATS subscription
  goroutine, so a slow lookup does not delay other requests. Requests beyond that are answered
  immediately with `service busy`; callers should retry with backoff. On shutdown the replica
  stops taking status and analytics requests and finishes the in-flight ones before it drains
  its NATS connection, all within one 25-second shutdown budget.

## Group Handles

Group tracking data is scoped by a service-issued group handle, not by a caller-chosen name:

- The handle is `grp_` followed by 32 lowercase hex characters (128 bits from `crypto/rand`), generated by `domain.NewGroupHandle` when `send_email` is called without `group_id`.
- It is returned only in the `send_email` reply. If the new group could not be recorded in `email-group-index` (KV write failure, or degraded mode), or a caller-supplied group reached `api.MaxGroupEmails` between the pre-send check and the append (see [Group Size and Read Bounds](#group-size-and-read-bounds)), the reply carries an empty `group_id` rather than a handle the email is not tracked under, and no recipient record is written; send the next email without `group_id` to start a new group. It is not written to the `X-LFX-TRACKING-ID` mail header and is not included in push events.
- `get_email_status` (including single-email lookups) and `get_email_engagement_analytics` require it. `send_email` accepts it only if the group index already has an entry for it, so only a holder of an issued handle can add sends to a group.
- Degraded mode: when NATS KV is unavailable at startup the service uses `NullTrackingStore`. It has no group index, so `send_email` cannot record new groups and replies with an empty `group_id` when none was supplied. It also cannot check supplied handles, so it accepts any well-formed one. Nothing is stored in this mode and status and analytics always reply `not found`, so no tracking data can be read or altered.
- Groups stored before handles were introduced are keyed by caller-chosen strings or UUIDs. Those values almost never match the handle format and are then rejected by all three subjects and their tracking data can no longer be read through the API. Before rolling this change out, confirm no existing `email-group-index` key already has the handle format (`nats kv ls email-group-index | grep -E '^grp_[0-9a-f]{32}$'` must print nothing): the earlier validation allowed callers to choose exactly that format, and such a key would be treated as an issued handle.
- Rollout: old and new pods must not overlap when this change ships. Old pods disclose the handle in mail headers and push events. Follow the required all-at-once procedure in `docs/service-helm-chart.md` § Group Handle Rollout.

## Change Checklist

- Update `pkg/api/nats.go`.
- Update handlers in `internal/service/`.
- Update this document.
- Update `docs/email-engagement-tracking.md` if the change touches tracking headers, KV fields, or SES events.
- Add or update table-driven tests.
- Run `make test` and `make check`.
