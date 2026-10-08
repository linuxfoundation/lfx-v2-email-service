// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package redaction

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactGroupHandle(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "grp_0123abcd****", RedactGroupHandle("grp_0123abcdef0123456789abcdef012345"))
	assert.Equal(t, "", RedactGroupHandle(""))
	assert.Equal(t, "sho****", RedactGroupHandle("short-value"))
}
