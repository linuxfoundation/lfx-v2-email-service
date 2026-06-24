// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"context"
	"testing"

	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestNatsHeaderCarrier_Get(t *testing.T) {
	t.Run("returns empty string for missing key", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		assert.Equal(t, "", carrier.Get("missing-key"))
	})

	t.Run("returns value for existing key", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		carrier.Set("traceparent", "00-trace-id-span-id-01")
		assert.Equal(t, "00-trace-id-span-id-01", carrier.Get("traceparent"))
	})

	t.Run("returns first value when multiple values set", func(t *testing.T) {
		header := make(natsgo.Header)
		header["traceparent"] = []string{"first", "second"}
		carrier := natsHeaderCarrier(header)
		assert.Equal(t, "first", carrier.Get("traceparent"))
	})

	t.Run("returns empty string for nil header", func(t *testing.T) {
		var carrier natsHeaderCarrier
		assert.Equal(t, "", carrier.Get("any-key"))
	})
}

func TestNatsHeaderCarrier_Set(t *testing.T) {
	t.Run("sets value on new key", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		carrier.Set("traceparent", "00-abc-def-01")
		assert.Equal(t, "00-abc-def-01", carrier.Get("traceparent"))
	})

	t.Run("overwrites existing value", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		carrier.Set("traceparent", "value1")
		carrier.Set("traceparent", "value2")
		assert.Equal(t, "value2", carrier.Get("traceparent"))
	})

	t.Run("stores full header state correctly", func(t *testing.T) {
		header := make(natsgo.Header)
		carrier := natsHeaderCarrier(header)
		carrier.Set("traceparent", "00-trace-span-01")
		carrier.Set("tracestate", "vendor=value")
		assert.Len(t, header, 2)
		assert.Equal(t, []string{"00-trace-span-01"}, header["traceparent"])
		assert.Equal(t, []string{"vendor=value"}, header["tracestate"])
	})
}

func TestNatsHeaderCarrier_Keys(t *testing.T) {
	t.Run("returns empty slice for empty header", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		assert.Empty(t, carrier.Keys())
	})

	t.Run("returns all keys for populated header", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		carrier.Set("key1", "v1")
		carrier.Set("key2", "v2")
		keys := carrier.Keys()
		assert.Len(t, keys, 2)
		assert.Contains(t, keys, "key1")
		assert.Contains(t, keys, "key2")
	})

	t.Run("returns empty slice for nil header", func(t *testing.T) {
		var carrier natsHeaderCarrier
		assert.Empty(t, carrier.Keys())
	})
}

func TestNatsHeaderCarrier_TextMapCarrier(t *testing.T) {
	t.Run("satisfies TextMapCarrier interface", func(t *testing.T) {
		var _ propagation.TextMapCarrier = natsHeaderCarrier{}
	})

	t.Run("Set/Get round-trip preserves values", func(t *testing.T) {
		header := make(natsgo.Header)
		carrier := natsHeaderCarrier(header)

		carrier.Set("traceparent", "00-trace-id-span-id-01")
		carrier.Set("tracestate", "vendor=value")

		assert.Equal(t, "00-trace-id-span-id-01", carrier.Get("traceparent"))
		assert.Equal(t, "vendor=value", carrier.Get("tracestate"))
		assert.Len(t, header, 2)
	})
}

func TestExtractAndStartConsumerSpan(t *testing.T) {
	// Set up an in-memory span exporter and a real TracerProvider.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	// Wire the test provider and a W3C propagator globally for the duration of the test.
	origTP := otel.GetTracerProvider()
	origProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		otel.SetTracerProvider(origTP)
		otel.SetTextMapPropagator(origProp)
	}()

	t.Run("creates consumer span with correct attributes", func(t *testing.T) {
		exporter.Reset()

		msg := &natsgo.Msg{
			Subject: "test.subject",
			Data:    []byte("hello"),
			Header:  make(natsgo.Header),
		}
		subject := "test.subject"

		ctx, span := ExtractAndStartConsumerSpan(context.Background(), msg, subject)
		require.NotNil(t, span)
		span.End()

		spans := exporter.GetSpans()
		require.Len(t, spans, 1)

		s := spans[0]
		assert.Equal(t, "nats.process", s.Name)
		assert.Equal(t, trace.SpanKindConsumer, s.SpanKind)

		attrMap := make(map[string]string)
		for _, a := range s.Attributes {
			attrMap[string(a.Key)] = a.Value.AsString()
		}
		assert.Equal(t, "nats", attrMap["messaging.system"])
		assert.Equal(t, subject, attrMap["messaging.destination.name"])
		assert.Equal(t, "process", attrMap["messaging.operation.type"])

		_ = ctx // ctx carries the span; consumed by the handler under test
	})

	t.Run("extracts parent trace context from message headers", func(t *testing.T) {
		exporter.Reset()

		// Inject a parent span context into a synthetic message header.
		parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "parent")
		parentSpan.End()
		parentSC := parentSpan.SpanContext()

		msg := &natsgo.Msg{
			Subject: "test.subject",
			Data:    []byte("payload"),
			Header:  make(natsgo.Header),
		}
		otel.GetTextMapPropagator().Inject(parentCtx, natsHeaderCarrier(msg.Header))

		_, span := ExtractAndStartConsumerSpan(context.Background(), msg, "test.subject")
		span.End()

		spans := exporter.GetSpans()
		require.Len(t, spans, 2) // parent + consumer

		// Find the consumer span by name to avoid ordering assumptions.
		var consumerSpan tracetest.SpanStub
		for _, s := range spans {
			if s.Name == "nats.process" {
				consumerSpan = s
				break
			}
		}
		require.Equal(t, "nats.process", consumerSpan.Name, "consumer span not found in exporter")
		assert.Equal(t, parentSC.TraceID(), consumerSpan.SpanContext.TraceID())
		assert.Equal(t, parentSC.SpanID(), consumerSpan.Parent.SpanID())
	})
}
