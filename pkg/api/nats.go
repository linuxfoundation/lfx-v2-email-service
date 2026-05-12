// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package api contains the public NATS contract for the email service.
// Resource services import only this package to know both the subject to
// publish to and the request/response payload shapes.
package api

const (
	// SendEmailSubject is the NATS request/reply subject for sending emails.
	// Callers publish a JSON-encoded SendEmailRequest and wait for a reply.
	// An empty reply body means success; a JSON SendEmailErrorResponse means failure.
	SendEmailSubject = "lfx.email-service.send_email"

	// QueueGroup is the NATS queue group used by email service subscribers.
	QueueGroup = "lfx.email-service.queue"
)

// SendEmailRequest is the JSON payload published to SendSubject.
// Callers render the HTML and plain-text bodies before publishing.
type SendEmailRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

// SendEmailErrorResponse is the JSON payload returned in the NATS reply on failure.
// A nil or empty reply body indicates success.
type SendEmailErrorResponse struct {
	Error string `json:"error"`
}
