// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
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

	// ErrAddressTooLong is returned by CheckAddressLength when an address field
	// exceeds the limits in pkg/api.
	ErrAddressTooLong = errors.New("address too long")
)

// CheckAddressLength bounds an address field before it is parsed further or
// used in a header or the SMTP envelope. It returns ErrAddressTooLong when raw
// exceeds api.MaxAddressFieldLength bytes (checked before parsing), or when raw
// parses as an address whose mailbox exceeds api.MaxAddressLength bytes or whose
// local part exceeds api.MaxAddressLocalPartLength bytes. A raw value within the
// field bound that does not parse returns nil, leaving malformed-address
// handling to the existing validation.
func CheckAddressLength(raw string) error {
	if len(raw) > api.MaxAddressFieldLength {
		return ErrAddressTooLong
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return nil
	}
	if len(addr.Address) > api.MaxAddressLength {
		return ErrAddressTooLong
	}
	if at := strings.LastIndex(addr.Address, "@"); at > api.MaxAddressLocalPartLength {
		return ErrAddressTooLong
	}
	return nil
}

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
// Returns ErrAddressMalformed when from cannot be parsed or its local part
// would require quoting or contains non-ASCII characters (see strictAddressDomain).
// Returns ErrFromDomainNotAllowed when the domain is absent from the allowlist.
func (p AddressPolicy) ValidateFrom(from string) error {
	if from == "" {
		return nil
	}
	domain, err := strictAddressDomain(from)
	if err != nil {
		return err
	}
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
// Returns ErrAddressMalformed when replyTo cannot be parsed or its local part
// would require quoting or contains non-ASCII characters (see strictAddressDomain).
// Returns ErrReplyToDomainNotAllowed when the domain is absent from the allowlist.
func (p AddressPolicy) ValidateReplyTo(replyTo string) error {
	if replyTo == "" {
		return nil
	}
	domain, err := strictAddressDomain(replyTo)
	if err != nil {
		return err
	}
	for _, d := range p.AllowedReplyToDomains {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return nil
		}
	}
	return ErrReplyToDomainNotAllowed
}

// strictAddressDomain parses raw as a single address and returns its lower-cased
// domain (the text after the final "@"). It returns ErrAddressMalformed when raw
// cannot be parsed or when the local part is not a plain dot-atom, i.e. it would
// need RFC 5322 quoting. mail.ParseAddress unescapes quoted local parts, so a
// quoted local part may contain "@", "," or "<"; rejecting them keeps the domain
// that is checked here identical to the mailbox written into headers and the
// SMTP envelope. Redundant quotes (e.g. `"alice"@example.org`) are removed by
// mail.ParseAddress and the normalised address is accepted; that normalised
// mailbox is what gets written, so the quotes carry no meaning. Non-ASCII addresses are rejected too: strings.ToLower folds
// some non-ASCII runes to ASCII (e.g. U+0130 to "i"), which would let the
// checked domain differ from the domain written into the message.
func strictAddressDomain(raw string) (string, error) {
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return "", ErrAddressMalformed
	}
	for i := 0; i < len(addr.Address); i++ {
		if addr.Address[i] >= utf8.RuneSelf {
			return "", ErrAddressMalformed
		}
	}
	at := strings.LastIndex(addr.Address, "@")
	if at <= 0 || at == len(addr.Address)-1 {
		return "", ErrAddressMalformed
	}
	// mail.Address.String re-quotes any local part that is not a dot-atom, so a
	// mismatch with the bare "<addr>" form means quoting would be required.
	if (&mail.Address{Address: addr.Address}).String() != "<"+addr.Address+">" {
		return "", ErrAddressMalformed
	}
	return strings.ToLower(addr.Address[at+1:]), nil
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
