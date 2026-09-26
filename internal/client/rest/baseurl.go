package rest

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// ValidateBaseURL checks a MotherDuck REST API base URL. The returned error
// describes the problem as a predicate, such as "must use the http or https
// scheme", so callers can prefix the setting name. Provider configuration and
// the client share this check so the api_base_url argument and the
// MOTHERDUCK_API_BASE_URL environment variable follow the same rules.
func ValidateBaseURL(raw string) error {
	if raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, " \t\r\n") {
		return errors.New("must not include whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("must be an absolute HTTP or HTTPS URL with a host, such as https://api.motherduck.com")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("must use the http or https scheme")
	}
	if parsed.User != nil {
		return errors.New("must not include username or password credentials")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return errors.New("must not include a query string or fragment")
	}
	if scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return errors.New("must use https unless the host is a loopback address such as localhost or 127.0.0.1, because the admin token would otherwise be sent in cleartext")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validateRequestPath rejects empty, "." and ".." segments. url.PathEscape
// leaves dot segments unchanged, and a proxy that normalizes paths could
// resolve them to a different API route than the caller intended.
func validateRequestPath(path string) error {
	pathOnly, _, _ := strings.Cut(path, "?")
	segments := strings.Split(strings.TrimPrefix(pathOnly, "/"), "/")
	for _, segment := range segments {
		switch segment {
		case "":
			return errors.New("MotherDuck API request path has an empty segment, so a required identifier is missing")
		case ".", "..":
			return errors.New(`MotherDuck API request identifiers must not be "." or ".."`)
		}
	}
	return nil
}
