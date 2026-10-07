package browser

import (
	"net/url"
	"strings"
)

// BrowserSecurityPolicy limits explicitly requested navigations to web URLs.
// It does not claim to be a network firewall or a redirect interception layer.
const securityPolicyVersion = 1

func validateNavigationURL(raw string, allowBlank bool) error {
	deny := func() error {
		return browserError(ErrPolicyDenied, "browser URL is not allowed by the navigation policy", "security_policy", &ErrorDetails{Field: "url"}, nil)
	}
	if raw == "" || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\\\r\n\t") {
		return deny()
	}
	if allowBlank && raw == "about:blank" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Opaque != "" || u.User != nil || u.Host == "" || u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		return deny()
	}
	if strings.HasSuffix(u.Host, ":") || strings.ContainsAny(u.Hostname(), " \t\r\n#?") {
		return deny()
	}
	// url.Parse defers port validation. Force it here without exposing URL values.
	_ = u.Port()
	return nil
}

func validateStorageOrigin(raw string) error {
	if err := validateNavigationURL(raw, false); err != nil {
		return err
	}
	u, _ := url.Parse(raw)
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return browserError(ErrPolicyDenied, "local_storage key must be an http(s) origin", "security_policy", &ErrorDetails{Field: "local_storage"}, nil)
	}
	return nil
}

func validateStartSecurity(req StartRequest) error {
	if err := validateNavigationURL(req.URL, true); err != nil {
		return err
	}
	for _, cookie := range req.Cookies {
		if cookie.URL != "" {
			if err := validateNavigationURL(cookie.URL, false); err != nil {
				return browserError(ErrPolicyDenied, "cookie URL violates browser policy", "security_policy", &ErrorDetails{Field: "cookies"}, nil)
			}
		}
	}
	for origin := range req.LocalStorage {
		if err := validateStorageOrigin(origin); err != nil {
			return err
		}
	}
	return nil
}

func validateActSecurity(actions []Action) error {
	for i, action := range actions {
		if action.Kind == "goto" && action.Goto != nil {
			if err := validateNavigationURL(action.Goto.URL, true); err != nil {
				return browserError(ErrPolicyDenied, "browser navigation is blocked by URL policy", "security_policy", &ErrorDetails{Field: "actions", ActionIndex: browserIntPointer(i)}, nil)
			}
		}
	}
	return nil
}
