// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRedactLink exercises redactLink directly via the white-box (same package)
// test to verify that query strings and fragments are stripped for every input
// class the function must handle.
func TestRedactLink(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		// Happy path: absolute HTTPS URL with query — the most common SES
		// redirect shape. Query must be gone; scheme, host, and path kept.
		{
			name: "absolute_https_with_query",
			in:   "https://click.example.com/r?token=secret&uid=abc",
			want: "https://click.example.com/r",
		},
		// Absolute URL that also has a fragment.
		{
			name: "absolute_https_with_fragment",
			in:   "https://example.com/page#section",
			want: "https://example.com/page",
		},
		// Both query and fragment present; both must be stripped.
		{
			name: "absolute_https_with_query_and_fragment",
			in:   "https://example.com/path?token=s3cr3t&ref=email#top",
			want: "https://example.com/path",
		},
		// Clean URL with no sensitive components — must pass through unchanged.
		{
			name: "absolute_https_clean",
			in:   "https://example.com/docs/guide",
			want: "https://example.com/docs/guide",
		},
		// HTTP scheme is also valid for SES tracked URLs.
		{
			name: "absolute_http_with_query",
			in:   "http://click.example.com/track?id=42",
			want: "http://click.example.com/track",
		},
		// Relative URL — url.Parse succeeds but Host is empty, so the fallback
		// path (strings.IndexAny) must strip the query.
		{
			name: "relative_url_with_query_fallback",
			in:   "/path/to/page?secret=x",
			want: "/path/to/page",
		},
		// Relative URL with fragment only — fallback must strip the fragment.
		{
			name: "relative_url_with_fragment_fallback",
			in:   "/path/to/page#section",
			want: "/path/to/page",
		},
		// Malformed URL: bare "javascript:…" scheme has no host; fallback must
		// treat '?' as the cut point if present, else return as-is.
		{
			name: "malformed_no_host",
			in:   "not-a-url?leaked=param",
			want: "not-a-url",
		},
		// Empty string must not panic; returns empty string.
		{
			name: "empty",
			in:   "",
			want: "",
		},
		// URL with no query or fragment; path must be unchanged.
		{
			name: "clean_no_trailing_sep",
			in:   "https://example.com",
			want: "https://example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := redactLink(tc.in)
			assert.Equal(t, tc.want, got, "redactLink(%q)", tc.in)
			// Regardless of input, the output must never contain '?' or '#'.
			assert.NotContains(t, got, "?", "result must not contain query marker")
			assert.NotContains(t, got, "#", "result must not contain fragment marker")
		})
	}
}
