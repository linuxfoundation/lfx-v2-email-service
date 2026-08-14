// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"net/mail"
	"strings"
)

// Sentinel errors returned by AddressPolicy validation methods.
var (
	// ErrAddressMalformed is returned when an address string cannot be parsed.
	ErrAddressMalformed = errors.New("malformed address")

	// ErrFromDomainNotAllowed is returned when the From domain is not in
	// AddressPolicy.AllowedFromDomains.
	ErrFromDomainNotAllowed = errors.New("from domain not allowed")

	// ErrReplyToDomainNotAllowed is returned when the Reply-To domain is not in
	// AddressPolicy.AllowedReplyToDomains.
	ErrReplyToDomainNotAllowed = errors.New("reply_to domain not allowed")
)

// AddressPolicy holds the three address allowlists for a send-email request.
// Construct it with NewAddressPolicy; do not build the struct literal directly
// — the constructor normalises domain strings (lowercase, trim, deduplicate empty).
//
// AllowedFromDomains is an exact-match list of domains permitted for per-message
// From overrides. An empty slice blocks all per-message From overrides.
//
// AllowedReplyToDomains is a list of base domains for Reply-To; subdomain suffix
// matching applies, so "linuxfoundation.org" also permits "lfx.linuxfoundation.org".
//
// AllowedRecipientDomains is a list of base domains for the recipient address;
// subdomain suffix matching applies. An empty slice permits all domains (production
// default); set it in non-prod to block real users' addresses.
type AddressPolicy struct {
	AllowedFromDomains      []string
	AllowedReplyToDomains   []string
	AllowedRecipientDomains []string
}

// NewAddressPolicy creates an AddressPolicy with normalised domain slices
// (lower-cased, trimmed, empty strings removed).
func NewAddressPolicy(fromDomains, replyToDomains, recipientDomains []string) AddressPolicy {
	return AddressPolicy{
		AllowedFromDomains:      normalizeDomains(fromDomains),
		AllowedReplyToDomains:   normalizeDomains(replyToDomains),
		AllowedRecipientDomains: normalizeDomains(recipientDomains),
	}
}

// IsRecipientAllowed reports whether to is a permitted recipient.
// Returns (true, nil) when AllowedRecipientDomains is empty (permit all) or when
// the address domain matches an allowed entry.
// Returns (false, ErrAddressMalformed) when to cannot be parsed as an address.
// Returns (false, nil) when the domain is absent from the allowlist.
func (p AddressPolicy) IsRecipientAllowed(to string) (bool, error) {
	if len(p.AllowedRecipientDomains) == 0 {
		return true, nil
	}
	addr, err := mail.ParseAddress(to)
	if err != nil {
		return false, ErrAddressMalformed
	}
	// Use LastIndex so RFC-valid quoted local parts containing "@" don't
	// cause mis-classification; the domain is always after the final "@".
	at := strings.LastIndex(addr.Address, "@")
	if at < 0 {
		return false, ErrAddressMalformed
	}
	domain := strings.ToLower(addr.Address[at+1:])
	for _, d := range p.AllowedRecipientDomains {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true, nil
		}
	}
	return false, nil
}

// ValidateFrom checks the per-message From override.
// Returns nil when from is empty (no override) or when its domain is in
// AllowedFromDomains.
// Returns ErrAddressMalformed when from cannot be parsed.
// Returns ErrFromDomainNotAllowed when the domain is absent from the allowlist.
func (p AddressPolicy) ValidateFrom(from string) error {
	if from == "" {
		return nil
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return ErrAddressMalformed
	}
	parts := strings.SplitN(addr.Address, "@", 2)
	if len(parts) != 2 {
		return ErrAddressMalformed
	}
	domain := strings.ToLower(parts[1])
	for _, d := range p.AllowedFromDomains {
		if d == domain {
			return nil
		}
	}
	return ErrFromDomainNotAllowed
}

// ValidateReplyTo checks the Reply-To address.
// Returns nil when replyTo is empty or when its domain matches an entry in
// AllowedReplyToDomains (subdomain suffix matching applies).
// Returns ErrAddressMalformed when replyTo cannot be parsed.
// Returns ErrReplyToDomainNotAllowed when the domain is absent from the allowlist.
func (p AddressPolicy) ValidateReplyTo(replyTo string) error {
	if replyTo == "" {
		return nil
	}
	addr, err := mail.ParseAddress(replyTo)
	if err != nil {
		return ErrAddressMalformed
	}
	parts := strings.SplitN(addr.Address, "@", 2)
	if len(parts) != 2 {
		return ErrAddressMalformed
	}
	domain := strings.ToLower(parts[1])
	for _, d := range p.AllowedReplyToDomains {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return nil
		}
	}
	return ErrReplyToDomainNotAllowed
}

// normalizeDomains returns a new slice of lower-cased, trimmed, non-empty domain strings.
func normalizeDomains(domains []string) []string {
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			out = append(out, d)
		}
	}
	return out
}
