// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
)

func TestAddressPolicy_IsRecipientAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		recipientDomains []string
		to               string
		wantAllowed      bool
		wantErr          error
	}{
		{
			name:             "empty allowlist permits all",
			recipientDomains: nil,
			to:               "user@anything.com",
			wantAllowed:      true,
		},
		{
			name:             "exact domain match",
			recipientDomains: []string{"linuxfoundation.org"},
			to:               "user@linuxfoundation.org",
			wantAllowed:      true,
		},
		{
			name:             "subdomain of allowed base domain",
			recipientDomains: []string{"linuxfoundation.org"},
			to:               "user@lfx.linuxfoundation.org",
			wantAllowed:      true,
		},
		{
			name:             "domain not in allowlist",
			recipientDomains: []string{"linuxfoundation.org"},
			to:               "user@gmail.com",
			wantAllowed:      false,
		},
		{
			name:             "malformed address",
			recipientDomains: []string{"linuxfoundation.org"},
			to:               "not-an-email",
			wantAllowed:      false,
			wantErr:          domain.ErrAddressMalformed,
		},
		{
			name:             "case-insensitive domain matching",
			recipientDomains: []string{"LinuxFoundation.ORG"},
			to:               "user@linuxfoundation.org",
			wantAllowed:      true,
		},
		{
			name:             "second entry in allowlist matches",
			recipientDomains: []string{"example.org", "linuxfoundation.org"},
			to:               "user@linuxfoundation.org",
			wantAllowed:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			policy := domain.NewAddressPolicy(nil, nil, tc.recipientDomains)
			allowed, err := policy.IsRecipientAllowed(tc.to)
			assert.Equal(t, tc.wantAllowed, allowed)
			if tc.wantErr != nil {
				assert.True(t, errors.Is(err, tc.wantErr), "want err %v, got %v", tc.wantErr, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAddressPolicy_ValidateFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fromDomains []string
		from        string
		wantErr     error
	}{
		{
			name:        "empty from is always permitted",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        "",
		},
		{
			name:        "domain in allowlist",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        "events@lfx.linuxfoundation.org",
		},
		{
			name:        "domain not in allowlist",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        "attacker@evil.com",
			wantErr:     domain.ErrFromDomainNotAllowed,
		},
		{
			name:        "malformed address",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        "not-an-email",
			wantErr:     domain.ErrAddressMalformed,
		},
		{
			name:        "empty allowlist blocks all from overrides",
			fromDomains: nil,
			from:        "someone@anywhere.com",
			wantErr:     domain.ErrFromDomainNotAllowed,
		},
		{
			name:        "case-insensitive allowlist matching",
			fromDomains: []string{"LFX.LINUXFOUNDATION.ORG"},
			from:        "events@lfx.linuxfoundation.org",
		},
		{
			name:        "subdomain does NOT match (exact-match semantics for from)",
			fromDomains: []string{"linuxfoundation.org"},
			from:        "events@sub.linuxfoundation.org",
			wantErr:     domain.ErrFromDomainNotAllowed,
		},
		{
			name:        "display name in from address is accepted",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        "LFX Events <events@lfx.linuxfoundation.org>",
		},
		{
			name:        "quoted local part containing @ is rejected",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        `"x@evil.com"@lfx.linuxfoundation.org`,
			wantErr:     domain.ErrAddressMalformed,
		},
		{
			name:        "redundantly quoted dot-atom local part is normalised and accepted",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        `"alice"@lfx.linuxfoundation.org`,
		},
		{
			name:        "non-ASCII domain that case-folds onto an allowed domain is rejected",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        "events@lfx.l\u0130nuxfoundation.org",
			wantErr:     domain.ErrAddressMalformed,
		},
		{
			name:        "address list in quoted local part is rejected",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        `"x@evil.com, y"@lfx.linuxfoundation.org`,
			wantErr:     domain.ErrAddressMalformed,
		},
		{
			name:        "quoted local part hiding an allowed domain is rejected",
			fromDomains: []string{"lfx.linuxfoundation.org"},
			from:        `"a@lfx.linuxfoundation.org"@evil.com`,
			wantErr:     domain.ErrAddressMalformed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			policy := domain.NewAddressPolicy(tc.fromDomains, nil, nil)
			err := policy.ValidateFrom(tc.from)
			if tc.wantErr != nil {
				assert.True(t, errors.Is(err, tc.wantErr), "want err %v, got %v", tc.wantErr, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAddressPolicy_ValidateReplyTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		replyToDomains []string
		replyTo        string
		wantErr        error
	}{
		{
			name:           "empty reply_to is always permitted",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        "",
		},
		{
			name:           "exact base domain match",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        "noreply@linuxfoundation.org",
		},
		{
			name:           "subdomain of allowed base domain",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        "support@lfx.linuxfoundation.org",
		},
		{
			name:           "domain not in allowlist",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        "attacker@gmail.com",
			wantErr:        domain.ErrReplyToDomainNotAllowed,
		},
		{
			name:           "malformed address",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        "not-an-email",
			wantErr:        domain.ErrAddressMalformed,
		},
		{
			name:           "case-insensitive matching",
			replyToDomains: []string{"LinuxFoundation.ORG"},
			replyTo:        "noreply@linuxfoundation.org",
		},
		{
			name:           "quoted local part with address list is rejected",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        `"x@evil.com, y"@lfx.linuxfoundation.org`,
			wantErr:        domain.ErrAddressMalformed,
		},
		{
			name:           "quoted local part containing @ is rejected",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        `"a@evil.com"@sub.linuxfoundation.org`,
			wantErr:        domain.ErrAddressMalformed,
		},
		{
			name:           "quoted local part hiding an allowed domain is rejected",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        `"a@lfx.linuxfoundation.org"@evil.com`,
			wantErr:        domain.ErrAddressMalformed,
		},
		{
			name:           "non-ASCII domain that case-folds onto an allowed domain is rejected",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        "attacker@l\u0130nuxfoundation.org",
			wantErr:        domain.ErrAddressMalformed,
		},
		{
			name:           "non-ASCII lookalike @ in local part is rejected",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        "attacker\uff20evil.com@lfx.linuxfoundation.org",
			wantErr:        domain.ErrAddressMalformed,
		},
		{
			name:           "redundantly quoted dot-atom local part is normalised and accepted",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        `"alice"@lfx.linuxfoundation.org`,
		},
		{
			name:           "display name with dot-atom address is accepted",
			replyToDomains: []string{"linuxfoundation.org"},
			replyTo:        "LFX Support <first.last+tag@lfx.linuxfoundation.org>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			policy := domain.NewAddressPolicy(nil, tc.replyToDomains, nil)
			err := policy.ValidateReplyTo(tc.replyTo)
			if tc.wantErr != nil {
				assert.True(t, errors.Is(err, tc.wantErr), "want err %v, got %v", tc.wantErr, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckAddressLength(t *testing.T) {
	t.Parallel()

	local64 := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{name: "empty", raw: ""},
		{name: "ordinary address", raw: "jane@example.com"},
		{name: "display name form", raw: "Jane Doe <jane@example.com>"},
		{name: "local part at limit", raw: local64 + "@example.com"},
		{name: "local part over limit", raw: local64 + "a@example.com", wantErr: domain.ErrAddressTooLong},
		{name: "address at limit", raw: "a@" + strings.Repeat("b", 252)},
		{name: "address over limit", raw: "a@" + strings.Repeat("b", 253), wantErr: domain.ErrAddressTooLong},
		{name: "raw field over limit, checked before parsing", raw: strings.Repeat("x", 513), wantErr: domain.ErrAddressTooLong},
		{name: "1 MiB local part", raw: strings.Repeat("a", 1<<20) + "@example.com", wantErr: domain.ErrAddressTooLong},
		{name: "unparseable within bound is left to other validation", raw: "not an address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, domain.CheckAddressLength(tt.raw), tt.wantErr)
			if tt.wantErr == nil {
				assert.NoError(t, domain.CheckAddressLength(tt.raw))
			}
		})
	}
}
