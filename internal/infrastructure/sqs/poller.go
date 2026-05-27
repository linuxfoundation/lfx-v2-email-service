// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package sqs provides a long-polling consumer for AWS SQS queues.
package sqs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// MessageHandler is called for each received SQS message.
// If it returns nil the message is deleted from the queue.
// If it returns an error the message is left in the queue to be retried or sent to the DLQ.
type MessageHandler func(ctx context.Context, msg types.Message) error

// Poller long-polls an SQS queue and dispatches messages to a handler.
type Poller struct {
	client               *sqs.Client
	queueURL             string
	maxConsecutiveErrors int
	handler              MessageHandler
}

// NewPoller creates a Poller that reads from queueURL.
// maxConsecutiveErrors is the number of consecutive ReceiveMessage failures allowed before
// Run returns — the caller should treat a return from Run as a fatal condition and exit.
func NewPoller(client *sqs.Client, queueURL string, maxConsecutiveErrors int, handler MessageHandler) *Poller {
	return &Poller{
		client:               client,
		queueURL:             queueURL,
		maxConsecutiveErrors: maxConsecutiveErrors,
		handler:              handler,
	}
}

// Run polls the queue until ctx is cancelled or consecutive errors exceed the limit.
// Each iteration requests up to 10 messages with a 20-second long-poll wait.
// Returns a non-nil error when the consecutive error limit is reached.
func (p *Poller) Run(ctx context.Context) error {
	consecutiveErrors := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		output, err := p.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(p.queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20,
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			consecutiveErrors++
			slog.ErrorContext(ctx, "sqs receive message failed",
				"error", err,
				"consecutive_errors", consecutiveErrors,
				"max", p.maxConsecutiveErrors,
			)
			if consecutiveErrors >= p.maxConsecutiveErrors {
				return fmt.Errorf("sqs poller aborting after %d consecutive errors: %w", consecutiveErrors, err)
			}
			// Linear backoff (1s × consecutive errors) capped at 30s to avoid hot-looping on persistent failures.
			backoff := time.Duration(consecutiveErrors) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			continue
		}

		consecutiveErrors = 0

		if len(output.Messages) > 0 {
			slog.InfoContext(ctx, "sqs received messages", "count", len(output.Messages))
		}

		for _, msg := range output.Messages {
			slog.InfoContext(ctx, "sqs processing message", "message_id", aws.ToString(msg.MessageId))
			if err := p.handler(ctx, msg); err != nil {
				slog.WarnContext(ctx, "sqs message handler failed, leaving in queue",
					"error", err,
					"message_id", aws.ToString(msg.MessageId),
				)
				continue
			}
			_, delErr := p.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(p.queueURL),
				ReceiptHandle: msg.ReceiptHandle,
			})
			if delErr != nil {
				slog.WarnContext(ctx, "failed to delete sqs message after processing",
					"error", delErr,
					"message_id", aws.ToString(msg.MessageId),
				)
			}
		}
	}
}
