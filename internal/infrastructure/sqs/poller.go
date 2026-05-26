// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package sqs provides a long-polling consumer for AWS SQS queues.
package sqs

import (
	"context"
	"log/slog"

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
	client   *sqs.Client
	queueURL string
	handler  MessageHandler
}

// NewPoller creates a Poller that reads from queueURL.
func NewPoller(client *sqs.Client, queueURL string, handler MessageHandler) *Poller {
	return &Poller{client: client, queueURL: queueURL, handler: handler}
}

// Run polls the queue until ctx is cancelled.
// Each iteration requests up to 10 messages with a 20-second long-poll wait.
func (p *Poller) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		output, err := p.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(p.queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20,
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "sqs receive message failed", "error", err)
			continue
		}

		for _, msg := range output.Messages {
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
