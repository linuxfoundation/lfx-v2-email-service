// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupOTelSDKSmoke(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")

	shutdown, err := SetupOTelSDK(context.Background())
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}
