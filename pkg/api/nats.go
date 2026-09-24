// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package api contains the public NATS contract for the email service.
// Resource services import only this package to know both the subject to
// publish to and the request/response payload shapes.
package api

import "time"

const (
	// SendEmailSubject is the NATS request/reply subject for sending emails.
	// On success the reply body is a JSON-encoded SendEmailResponse.
	// On failure the reply body is a JSON-encoded SendEmailErrorResponse.
	SendEmailSubject = "lfx.email-service.send_email"

	// QueueGroup is the NATS queue group used by email service subscribers.
	QueueGroup = "lfx.email-service.queue"

	// GetEmailStatusSubject is the NATS request/reply subject for fetching a
	// single recipient record by email_id.
	GetEmailStatusSubject = "lfx.email-service.get_email_status"

	// GetEmailEngagementAnalyticsSubject is the NATS request/reply subject for
	// fetching aggregate engagement counts for a group of emails.
	GetEmailEngagementAnalyticsSubject = "lfx.email-service.get_email_engagement_analytics"

	// EmailRecipientsKVBucket is the NATS KV bucket that stores one record per
	// sent email, keyed by email_id.
	EmailRecipientsKVBucket = "email-recipients"

	// EmailGroupIndexKVBucket is the NATS KV bucket that maps a group_id to the
	// list of email_ids belonging to that group.
	EmailGroupIndexKVBucket = "email-group-index"

	// EmailFailedSubject is the NATS subject the email service publishes to when
	// a sent email bounces or receives a spam complaint from SES. Callers may
	// subscribe to receive real-time failure notifications without polling
	// get_email_status. The payload is a JSON-encoded EmailFailedEvent.
	// Publishing is best-effort: a publish failure is logged but does not affect
	// the KV store update.
	EmailFailedSubject = "lfx.email-service.email_failed"

	// EmailDeliveredSubject is the NATS subject the email service publishes to
	// when SES confirms delivery of a sent email. The payload is a
	// JSON-encoded EmailDeliveredEvent.
	EmailDeliveredSubject = "lfx.email-service.email_delivered"

	// EmailOpenedSubject is the NATS subject the email service publishes to
	// when the open-tracking pixel in a sent email is loaded. The payload is a
	// JSON-encoded EmailOpenedEvent. Multiple opens produce multiple publishes;
	// callers should deduplicate by email_id if they only need a first-open
	// signal.
	EmailOpenedSubject = "lfx.email-service.email_opened"

	// EmailLinkClickedSubject is the NATS subject the email service publishes
	// to when a tracked link in a sent email is clicked. The payload is a
	// JSON-encoded EmailLinkClickedEvent. Multiple clicks (different links or
	// repeated clicks) each produce a separate publish.
	EmailLinkClickedSubject = "lfx.email-service.email_link_clicked"
)

// SendEmailRequest is the JSON payload published to SendEmailSubject.
// Callers render the HTML and plain-text bodies before publishing.
//
// From is optional. When set, the email is sent from that address instead of
// the service-level default (DEFAULT_SMTP_FROM). The domain must be in the service's
// allowed domain list (SMTP_ALLOWED_FROM_DOMAINS); a disallowed domain is
// rejected with an error response.
//
// FromDisplayName is optional. When set, it is used as the display name in the
// From header (e.g. "My Team <from@lfx.linuxfoundation.org>"). Defaults to the
// service-level DEFAULT_SMTP_FROM_DISPLAY_NAME (default: "LFX Self Serve").
//
// ReplyTo is optional. When set, it is written as the Reply-To SMTP header so
// that mail client replies are directed to this address instead of the From address.
// Must be a valid email address whose domain is in the service's reply-to allowlist
// (SMTP_ALLOWED_REPLY_TO_DOMAINS, default: "linuxfoundation.org"). Subdomain suffix
// matching applies, so "linuxfoundation.org" also permits "lfx.linuxfoundation.org".
type SendEmailRequest struct {
	To              string `json:"to"`
	Subject         string `json:"subject"`
	HTML            string `json:"html"`
	Text            string `json:"text"`
	From            string `json:"from,omitempty"`              // bare address; empty → service default
	FromDisplayName string `json:"from_display_name,omitempty"` // display name; empty → service default
	ReplyTo         string `json:"reply_to,omitempty"`          // Reply-To header address; omitted when empty
	GroupID         string `json:"group_id,omitempty"`
}

// SendEmailResponse is the JSON payload returned in the NATS reply on success.
type SendEmailResponse struct {
	EmailID string `json:"email_id"`
	GroupID string `json:"group_id"`
}

// SendEmailErrorResponse is the JSON payload returned in the NATS reply on failure.
type SendEmailErrorResponse struct {
	Error string `json:"error"`
}

// OpenEvent records a single open of an email, keyed by the SNS MessageId so
// replayed deliveries can be deduplicated.
type OpenEvent struct {
	EventID  string    `json:"event_id"`
	OpenedAt time.Time `json:"opened_at"`
}

// ClickEvent records a single tracked-link click, keyed by the SNS MessageId
// so replayed deliveries can be deduplicated.
type ClickEvent struct {
	EventID   string    `json:"event_id"`
	Link      string    `json:"link"`
	ClickedAt time.Time `json:"clicked_at"`
}

// EmailRecipientRecord is the value stored in EmailRecipientsKVBucket, keyed by email_id.
type EmailRecipientRecord struct {
	GroupID      string      `json:"group_id"`
	EmailID      string      `json:"email_id"`
	To           string      `json:"to"`
	Subject      string      `json:"subject"`
	SentAt       time.Time   `json:"sent_at"`
	Delivered    bool        `json:"delivered"`
	DeliveredAt  *time.Time  `json:"delivered_at,omitempty"`
	Opened       bool        `json:"opened"`
	OpenCount    int         `json:"open_count"`
	OpenedAtList []OpenEvent `json:"opened_at_list,omitempty"`
	LastOpenedAt *time.Time  `json:"last_opened_at,omitempty"`
	Clicked      bool        `json:"clicked"`
	ClickCount   int         `json:"click_count"`
	// ClickEventIDs holds the SNS MessageId for replay deduplication of CLICK
	// events. It is capped at 500 entries and, together with ClickList, must
	// keep the total serialised record under the KV bucket size limit
	// (~50 KB soft ceiling). Once both collections are at capacity, new click
	// MessageIds cannot be stored and SQS replays of those later clicks will
	// re-increment ClickCount — this is an explicit bounded-dedup-window
	// trade-off. Populated from this version of the service onward; older
	// records fall back to ClickList for dedup.
	ClickEventIDs []string     `json:"click_event_ids,omitempty"`
	ClickList     []ClickEvent `json:"click_list,omitempty"`
	LastClickedAt *time.Time   `json:"last_clicked_at,omitempty"`
	Failed        bool         `json:"failed"`
	FailedAt      *time.Time   `json:"failed_at,omitempty"`
}

// GetEmailStatusRequest is the payload for GetEmailStatusSubject.
// Exactly one of EmailID or GroupID must be set.
// When EmailID is set the reply is a single EmailRecipientRecord.
// When GroupID is set the reply is a JSON array of EmailRecipientRecord values.
type GetEmailStatusRequest struct {
	EmailID string `json:"email_id,omitempty"`
	GroupID string `json:"group_id,omitempty"`
}

// GetEmailEngagementAnalyticsRequest is the payload for GetEmailEngagementAnalyticsSubject.
type GetEmailEngagementAnalyticsRequest struct {
	GroupID string `json:"group_id"`
}

// GetEmailEngagementAnalyticsResponse is the reply for GetEmailEngagementAnalyticsSubject.
type GetEmailEngagementAnalyticsResponse struct {
	GroupID      string `json:"group_id"`
	TotalSent    int    `json:"total_sent"`
	Delivered    int    `json:"delivered"`
	Opened       int    `json:"opened"`
	UniqueOpened int    `json:"unique_opened"`
	Failed       int    `json:"failed"`
}

// EmailFailedEvent is the payload published to EmailFailedSubject when a sent
// email bounces or receives a spam complaint. Callers that stored the email_id
// returned by send_email can correlate this event back to the original send
// without polling get_email_status.
type EmailFailedEvent struct {
	EmailID  string    `json:"email_id"`
	GroupID  string    `json:"group_id,omitempty"`
	Reason   string    `json:"reason"` // "bounce" or "complaint"
	FailedAt time.Time `json:"failed_at"`
}

// EmailDeliveredEvent is the payload published to EmailDeliveredSubject when
// SES confirms that a sent email was successfully delivered to the recipient's
// mail server.
type EmailDeliveredEvent struct {
	EmailID     string    `json:"email_id"`
	GroupID     string    `json:"group_id,omitempty"`
	DeliveredAt time.Time `json:"delivered_at"`
}

// EmailOpenedEvent is the payload published to EmailOpenedSubject when the
// open-tracking pixel in a sent email is loaded by the recipient's mail client.
// OpenCount reflects the total number of opens recorded for this email_id,
// including the current one.
type EmailOpenedEvent struct {
	EmailID   string    `json:"email_id"`
	GroupID   string    `json:"group_id,omitempty"`
	OpenCount int       `json:"open_count"`
	OpenedAt  time.Time `json:"opened_at"`
}

// EmailLinkClickedEvent is the payload published to EmailLinkClickedSubject
// when a tracked link in a sent email is clicked. Link is the destination URL
// that was clicked. ClickCount reflects the total number of link clicks
// recorded for this email_id, including the current one.
type EmailLinkClickedEvent struct {
	EmailID    string    `json:"email_id"`
	GroupID    string    `json:"group_id,omitempty"`
	Link       string    `json:"link"`
	ClickCount int       `json:"click_count"`
	ClickedAt  time.Time `json:"clicked_at"`
}
