// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package api contains the public NATS contract for the email service.
// Resource services import only this package to know both the subject to
// publish to and the request/response payload shapes.
package api

import "time"

const (
	// SendEmailSubject is the NATS request/reply subject for sending emails.
	// Callers publish a JSON-encoded SendEmailRequest and wait for a reply.
	// An empty reply body means success; a JSON SendEmailErrorResponse means failure.
	SendEmailSubject = "lfx.email-service.send_email"

	// QueueGroup is the NATS queue group used by email service subscribers.
	QueueGroup = "lfx.email-service.queue"

	// EmailOpenedSubject is the NATS subject published when an email open event is received.
	EmailOpenedSubject = "lfx.email-service.email-opened"

	// GetEmailStatusSubject is the NATS request/reply subject for querying email tracking state.
	GetEmailStatusSubject = "lfx.email-service.get_email_status"

	// EmailOpenTrackingKVBucket is the NATS KV bucket name for per-message tracking records.
	EmailOpenTrackingKVBucket = "email-open-tracking"
)

// SendEmailRequest is the JSON payload published to SendEmailSubject.
// Callers render the HTML and plain-text bodies before publishing.
type SendEmailRequest struct {
	To            string `json:"to"`
	Subject       string `json:"subject"`
	HTML          string `json:"html"`
	Text          string `json:"text"`
	CorrelationID string `json:"correlation_id,omitempty"`
	SourceService string `json:"source_service,omitempty"`
}

// SendEmailErrorResponse is the JSON payload returned in the NATS reply on failure.
// A nil or empty reply body indicates success.
type SendEmailErrorResponse struct {
	Error string `json:"error"`
}

// EmailTrackingRecord is the NATS KV value stored per sent email.
// Keyed by the Message-ID header (without angle brackets) under bucket EmailOpenTrackingKVBucket.
type EmailTrackingRecord struct {
	SESMessageID  string     `json:"ses_message_id"`
	CorrelationID string     `json:"correlation_id,omitempty"`
	SourceService string     `json:"source_service,omitempty"`
	To            string     `json:"to"`
	Subject       string     `json:"subject"`
	SentAt        time.Time  `json:"sent_at"`
	OpenCount     int        `json:"open_count"`
	FirstOpenedAt *time.Time `json:"first_opened_at,omitempty"`
	LastOpenedAt  *time.Time `json:"last_opened_at,omitempty"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	BouncedAt     *time.Time `json:"bounced_at,omitempty"`
	ComplainedAt  *time.Time `json:"complained_at,omitempty"`
}

// GetEmailStatusRequest is the payload for GetEmailStatusSubject.
// Exactly one of SESMessageID or CorrelationID must be set.
type GetEmailStatusRequest struct {
	SESMessageID  string `json:"ses_message_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}
