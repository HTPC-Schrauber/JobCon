package nexus

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var (
	// SafeNexusURLRegex strictly validates the structure of a Nexus Base URL:
	// - Must begin with http:// or https://
	// - Host may be an IPv6 address in brackets, or an IPv4/hostname containing alphanumeric, dots, hyphens, underscores
	// - Optional port (:1-65535)
	// - Optional path segments containing alphanumerics, dots, hyphens, underscores, tildes, or percent-encoded sequences
	// - Prohibits userinfo, queries, fragments, control characters, and spaces.
	SafeNexusURLRegex = regexp.MustCompile(`^https?://(?:\[[0-9a-fA-F:]+\]|[a-zA-Z0-9_.-]+)(?::[0-9]{1,5})?(?:/[a-zA-Z0-9_.\-~%]*)*/*$`)
)

// ValidateNexusURL ensures that the given rawURL is a safe HTTP or HTTPS URL suitable for Nexus requests,
// preventing Server-Side Request Forgery (SSRF) and access to cloud metadata services.
func ValidateNexusURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errors.New("nexus Basis-URL darf nicht leer sein")
	}
	if len(rawURL) > 2048 {
		return errors.New("nexus Basis-URL ist zu lang")
	}
	if !SafeNexusURLRegex.MatchString(rawURL) {
		return errors.New("ungültiges URL-Format: nur HTTP- oder HTTPS-URLs mit gültigem Hostnamen und Port sind erlaubt")
	}

	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("ungültige Basis-URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("ungültiges Schema: nur http und https sind erlaubt")
	}
	if u.Host == "" {
		return errors.New("basis-URL enthält keinen Host")
	}
	if u.User != nil {
		return errors.New("anmeldedaten in der Basis-URL sind nicht erlaubt")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("query-Parameter oder Fragmente sind in der Basis-URL nicht erlaubt")
	}

	hostname := u.Hostname()
	if isBlockedHost(hostname) {
		return errors.New("zugriff auf diesen Host ist aus Sicherheitsgründen nicht gestattet")
	}

	return nil
}

// isBlockedHost checks whether the given host is a known cloud metadata endpoint or link-local address.
func isBlockedHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	// Cloud metadata hostnames
	if host == "169.254.169.254" || host == "metadata.google.internal" || host == "instance-data" {
		return true
	}
	// Check link-local IP range (RFC 3927 / IPv6 link-local)
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return true
		}
	}
	return false
}
