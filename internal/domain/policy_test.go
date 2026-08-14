// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
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
