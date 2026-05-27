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
type SendEmailRequest struct {
	To            string `json:"to"`
	Subject       string `json:"subject"`
	HTML          string `json:"html"`
	Text          string `json:"text"`
	GroupID       string `json:"group_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	SourceService string `json:"source_service,omitempty"`
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

// EmailRecipientRecord is the value stored in EmailRecipientsKVBucket, keyed by email_id.
type EmailRecipientRecord struct {
	GroupID     string     `json:"group_id"`
	EmailID     string     `json:"email_id"`
	To          string     `json:"to"`
	Subject     string     `json:"subject"`
	SentAt      time.Time  `json:"sent_at"`
	Delivered   bool       `json:"delivered"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	Opened      bool       `json:"opened"`
	OpenedAt    *time.Time `json:"opened_at,omitempty"`
	Failed      bool       `json:"failed"`
	FailedAt    *time.Time `json:"failed_at,omitempty"`
}

// GetEmailStatusRequest is the payload for GetEmailStatusSubject.
type GetEmailStatusRequest struct {
	EmailID string `json:"email_id"`
}

// GetEmailEngagementAnalyticsRequest is the payload for GetEmailEngagementAnalyticsSubject.
type GetEmailEngagementAnalyticsRequest struct {
	GroupID string `json:"group_id"`
}

// GetEmailEngagementAnalyticsResponse is the reply for GetEmailEngagementAnalyticsSubject.
type GetEmailEngagementAnalyticsResponse struct {
	GroupID   string `json:"group_id"`
	TotalSent int    `json:"total_sent"`
	Delivered int    `json:"delivered"`
	Opened    int    `json:"opened"`
	Failed    int    `json:"failed"`
}
