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
	Failed       bool        `json:"failed"`
	FailedAt     *time.Time  `json:"failed_at,omitempty"`
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
