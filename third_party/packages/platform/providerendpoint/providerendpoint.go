// Package providerendpoint is the ONE place the ASAO platform decides whether a provider
// (Odoo/Medusa/any future engine) endpoint is safe to dial. It exists so the SSRF policy — reject
// loopback, link-local, private, unique-local, multicast and unspecified addresses unless an
// operator explicitly opts in — is defined exactly once and consumed identically by every provider
// family, rather than duplicated (and inevitably drifting) per adapter.
//
// Two entry points, for two different moments:
//
//   - ValidateSyntax is pure and does no I/O: it parses the endpoint, checks the scheme, and — only
//     when the host is already a literal IP address — checks the address policy. It never resolves
//     a hostname, so it is safe to call from code that must stay I/O-free (e.g. domain model
//     Validate() methods). This is the CREATE-time gate.
//   - CheckHost additionally resolves a hostname via DNS and checks every resolved address. It does
//     real network I/O and belongs at connection-construction ("use") time, so that a
//     ProviderConnection row written before this policy existed cannot bypass it merely because it
//     names a hostname instead of a literal IP.
//
// Both are governed by the same opt-ins. AllowPrivateEnvVar lifts the policy for every endpoint: every
// accepted LAB composition (scripts/dev/asao-lab-up.sh, scripts/dev/medusa-lab-up.sh) legitimately
// points Odoo and Medusa connections at 127.0.0.1. PrivateAllowlistEnvVar lifts it for an exact list
// of host:port pairs only: production runs its own provider (the cluster's Odoo at odoo:8069) on a
// private Service address, and opening every private address to every organization to reach it would
// be the SSRF this package exists to prevent.
package providerendpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// AllowPrivateEnvVar is the operator opt-in that permits a provider endpoint resolving to a
// loopback/link-local/private/unique-local/multicast/unspecified address. Unset (or any value other
// than "true") means the policy is enforced.
const AllowPrivateEnvVar = "ASAO_ALLOW_PRIVATE_PROVIDER_ENDPOINTS"

// PrivateAllowlistEnvVar names the operator's exact list of private provider endpoints, as
// comma-separated host:port pairs (for example "odoo:8069"). An endpoint whose host and port (the
// scheme's default when the URL names none) match an entry is accepted although it is, or resolves to,
// a private address. Matching is on the host as written in the endpoint, case-insensitive; nothing
// else about private addresses changes.
const PrivateAllowlistEnvVar = "ASAO_PRIVATE_PROVIDER_ENDPOINTS"

// ErrInvalidEndpoint reports an endpoint that is not a well-formed http(s) URL at all — wrong or
// missing scheme, unparseable, or missing host.
var ErrInvalidEndpoint = errors.New("provider endpoint: must be a well-formed http or https URL")

// ErrEndpointNotPermitted reports an endpoint whose host — literally, or after DNS resolution —
// is loopback, link-local, private (RFC 1918 / RFC 4193), multicast, or unspecified, and the
// AllowPrivateEnvVar opt-in was not set.
var ErrEndpointNotPermitted = errors.New("provider endpoint: resolves to a disallowed network address")

// Allowed reports whether the operator has explicitly opted into private/loopback provider
// endpoints.
func Allowed() bool {
	return os.Getenv(AllowPrivateEnvVar) == "true"
}

// allowlisted reports whether u's host:port is one of the operator's PrivateAllowlistEnvVar entries.
func allowlisted(u *url.URL) bool {
	raw := os.Getenv(PrivateAllowlistEnvVar)
	if strings.TrimSpace(raw) == "" {
		return false
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	want := strings.ToLower(net.JoinHostPort(u.Hostname(), port))
	for _, entry := range strings.Split(raw, ",") {
		if strings.ToLower(strings.TrimSpace(entry)) == want {
			return true
		}
	}
	return false
}

// disallowed is the single CIDR/address-class policy every caller in this package enforces. It
// covers every range FIX 1(b) names: loopback, link-local unicast/multicast (169.254.0.0/16 and
// fe80::/10), private (RFC 1918) and unique-local (RFC 4193, fc00::/7) via net.IP.IsPrivate,
// multicast, and unspecified (0.0.0.0 / ::).
func disallowed(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// parse validates scheme and host shape only. It returns the parsed URL and its hostname, or a
// wrapped ErrInvalidEndpoint.
func parse(endpoint string) (*url.URL, string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidEndpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", fmt.Errorf("%w: scheme must be http or https, got %q", ErrInvalidEndpoint, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, "", fmt.Errorf("%w: endpoint has no host", ErrInvalidEndpoint)
	}
	return u, host, nil
}

// ValidateSyntax parses endpoint, requires scheme http/https, and — only when the host is already a
// literal IP address — enforces the disallowed-address policy. It performs NO DNS resolution and
// therefore no I/O of any kind, so it is safe to call from pure validation code. A hostname (as
// opposed to a literal IP) is accepted here unconditionally; the same host is checked again, WITH
// resolution, by CheckHost at connection-construction time.
func ValidateSyntax(endpoint string) error {
	u, host, err := parse(endpoint)
	if err != nil {
		return err
	}
	if Allowed() || allowlisted(u) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && disallowed(ip) {
		return fmt.Errorf("%w: %s", ErrEndpointNotPermitted, host)
	}
	return nil
}

// CheckHost performs the full, use-time check: parse + scheme, then resolve the host (a no-op
// resolution when the host is already a literal IP) and check every resolved address against the
// disallowed-address policy. This is real network I/O — call it once, at the point a provider
// adapter/client is actually constructed, never on a hot path that must stay I/O-free.
func CheckHost(ctx context.Context, endpoint string) error {
	return checkHost(ctx, endpoint, true)
}

// CheckPublicHost is CheckHost for an address a person or an agent typed, not a provider connection an
// operator configured (`asao presence check` fetches any page it is given, DEC-2026-0193). It honours the
// lab-wide opt-in AllowPrivateEnvVar but never PrivateAllowlistEnvVar: that list opens the platform's own
// private providers (production's odoo:8069) to provider connections, and a URL anyone may type must not
// reach them.
func CheckPublicHost(ctx context.Context, endpoint string) error {
	return checkHost(ctx, endpoint, false)
}

func checkHost(ctx context.Context, endpoint string, honourAllowlist bool) error {
	u, host, err := parse(endpoint)
	if err != nil {
		return err
	}
	if Allowed() || (honourAllowlist && allowlisted(u)) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if disallowed(ip) {
			return fmt.Errorf("%w: %s", ErrEndpointNotPermitted, host)
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("%w: host %q could not be resolved: %v", ErrEndpointNotPermitted, host, err)
	}
	for _, addr := range addrs {
		if disallowed(addr.IP) {
			return fmt.Errorf("%w: %s resolves to %s", ErrEndpointNotPermitted, host, addr.IP)
		}
	}
	return nil
}

// CheckRedirect is an http.Client.CheckRedirect implementation shared by every provider HTTP
// client: it refuses a redirect whose target host differs from the request's original host, and
// preserves the standard library's 10-redirect cap. A provider that answers with a same-host
// redirect (e.g. http -> https on itself) still works; a provider redirecting a caller's credentials
// to a different host — the classic SSRF/credential-leak vector this closes — does not.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("provider endpoint: stopped after 10 redirects")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("provider endpoint: redirect from %q to %q crosses host, refused", via[0].URL.Host, req.URL.Host)
	}
	return nil
}
