package gm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// RequiredCookieNames are the Google session cookies that libgm needs before it
// can talk to the Google Messages backend for a Google account. The upstream
// mautrix-gmessages login step declares the same set as required.
var RequiredCookieNames = []string{"SID", "HSID", "OSID", "SSID", "APISID", "SAPISID"}

// cookieAttributes are the DevTools cookie-table attributes that must not be
// mistaken for cookie name/value pairs when a user pastes a raw table or a
// semicolon-separated header that includes attributes.
var cookieAttributes = map[string]struct{}{
	"path":        {},
	"domain":      {},
	"expires":     {},
	"max-age":     {},
	"secure":      {},
	"httponly":    {},
	"samesite":    {},
	"priority":    {},
	"partitioned": {},
}

var (
	// curlHeaderPattern matches -H/--header arguments in either quote style.
	curlHeaderPattern = regexp.MustCompile(`(?is)(?:^|\s)(?:-H|--header)\s+(?:"([^"]*)"|'([^']*)')`)
	// curlCookiePattern matches -b/--cookie arguments in either quote style.
	curlCookiePattern = regexp.MustCompile(`(?is)(?:^|\s)(?:-b|--cookie)\s+(?:"([^"]*)"|'([^']*)')`)
	// rawCookieHeaderPattern matches a raw "Cookie: ..." header line.
	rawCookieHeaderPattern = regexp.MustCompile(`(?im)^\s*cookie\s*:\s*(.+)$`)
)

// ParseCookies turns the common ways a user can copy Google session cookies
// into a name/value map. It accepts:
//
//  1. A flat JSON object: {"SID":"...","HSID":"..."}
//  2. A JSON object that wraps cookies: {"cookies":{"SID":"..."}} or {"cookies":[{"name":"SID","value":"..."}]}
//  3. A JSON array from cookie export tools: [{"name":"SID","value":"..."}]
//  4. A cURL command copied from browser DevTools (Network tab, "Copy as cURL").
//  5. A raw "Cookie: ..." header line.
//  6. Raw semicolon-separated pairs: SID=...; HSID=...
//
// Extra cookies are preserved. Required cookies are validated. Error messages
// name only the missing cookies and never include cookie values.
func ParseCookies(input string) (map[string]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, errors.New("no cookie input provided")
	}

	var cookies map[string]string
	var err error

	switch {
	case strings.HasPrefix(input, "{"), strings.HasPrefix(input, "["):
		cookies, err = parseCookieJSON(input)
	case looksLikeCurl(input):
		cookies, err = parseCookieCurl(input)
	default:
		cookies, err = parseCookiePairs(input)
	}
	if err != nil {
		return nil, err
	}

	if err := validateRequiredCookies(cookies); err != nil {
		return nil, err
	}
	return cookies, nil
}

func looksLikeCurl(input string) bool {
	lower := strings.ToLower(input)
	return strings.Contains(lower, "curl ") || strings.HasPrefix(lower, "curl\n") ||
		strings.Contains(lower, "cookie:")
}

func parseCookieJSON(input string) (map[string]string, error) {
	var raw any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return nil, fmt.Errorf("cookie JSON is not valid JSON: %w", err)
	}

	switch v := raw.(type) {
	case map[string]any:
		// A wrapper object may hold the cookie map or array under "cookies".
		if nested, ok := v["cookies"]; ok {
			if m, err := cookiesFromAny(nested); err == nil {
				return m, nil
			}
		}
		return cookiesFromMap(v)
	case []any:
		return cookiesFromArray(v)
	default:
		return nil, errors.New("cookie JSON must be an object or an array")
	}
}

func cookiesFromAny(v any) (map[string]string, error) {
	switch t := v.(type) {
	case map[string]any:
		return cookiesFromMap(t)
	case []any:
		return cookiesFromArray(t)
	default:
		return nil, errors.New("cookies value must be an object or an array")
	}
}

func cookiesFromMap(m map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("cookie %q must have a string value", k)
		}
		if s = strings.TrimSpace(s); s != "" {
			out[strings.TrimSpace(k)] = s
		}
	}
	if len(out) == 0 {
		return nil, errors.New("cookie JSON contained no usable cookies")
	}
	return out, nil
}

func cookiesFromArray(items []any) (map[string]string, error) {
	out := make(map[string]string, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cookie array item %d must be an object", i)
		}
		name := firstString(obj, "name", "Name", "cookie", "key", "Key")
		value := firstString(obj, "value", "Value", "val", "Val")
		if name == "" || value == "" {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil, errors.New("cookie array contained no name/value pairs")
	}
	return out, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseCookieCurl(input string) (map[string]string, error) {
	var headerValue string

	for _, match := range curlHeaderPattern.FindAllStringSubmatch(input, -1) {
		header := match[1]
		if header == "" {
			header = match[2]
		}
		header = strings.TrimSpace(header)
		if strings.HasPrefix(strings.ToLower(header), "cookie:") {
			headerValue = strings.TrimSpace(header[len("cookie:"):])
			break
		}
	}

	if headerValue == "" {
		if match := curlCookiePattern.FindStringSubmatch(input); match != nil {
			headerValue = match[1]
			if headerValue == "" {
				headerValue = match[2]
			}
		}
	}

	if headerValue == "" {
		if match := rawCookieHeaderPattern.FindStringSubmatch(input); match != nil {
			headerValue = match[1]
		}
	}

	if headerValue == "" {
		return nil, errors.New("could not find a Cookie header in the cURL command; copy the request with 'Copy as cURL' and include the -H 'Cookie: ...' argument")
	}

	cookies, err := parseCookiePairs(headerValue)
	if err != nil {
		return nil, err
	}
	return cookies, nil
}

func parseCookiePairs(input string) (map[string]string, error) {
	// A raw "Cookie:" prefix can appear when users copy only the header.
	input = rawCookieHeaderPattern.ReplaceAllString(input, "$1")

	// Semicolons and newlines separate pairs. Commas are also accepted because
	// some cookie exports format one pair per line with a trailing comma.
	replacer := strings.NewReplacer("\r", ";", "\n", ";", ",", ";")
	parts := strings.Split(replacer.Replace(input), ";")

	out := make(map[string]string, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if name == "" || value == "" {
			continue
		}
		if _, isAttr := cookieAttributes[strings.ToLower(name)]; isAttr {
			continue
		}
		out[name] = value
	}

	if len(out) == 0 {
		return nil, errors.New("no name=value cookie pairs found in input")
	}
	return out, nil
}

func validateRequiredCookies(cookies map[string]string) error {
	if len(cookies) == 0 {
		return errors.New("no cookies provided")
	}
	missing := make([]string, 0, len(RequiredCookieNames))
	for _, name := range RequiredCookieNames {
		if strings.TrimSpace(cookies[name]) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required Google cookies: %s (found %d cookies; copy the request from DevTools with 'Copy as cURL' so all cookies are included)", strings.Join(missing, ", "), len(cookies))
	}
	return nil
}

// SetCookies stores Google session cookies on the client's auth data.
func (c *Client) SetCookies(cookies map[string]string) {
	if c == nil || c.authData == nil {
		return
	}
	c.authData.SetCookies(cookies)
}

// HasCookies reports whether the client has Google session cookies loaded.
// It checks the cookie map directly because libgm's AuthData.HasCookies also
// returns true for non-Google (phone/QR) accounts, which never have cookies.
func (c *Client) HasCookies() bool {
	if c == nil || c.authData == nil {
		return false
	}
	c.authData.CookiesLock.RLock()
	defer c.authData.CookiesLock.RUnlock()
	return len(c.authData.Cookies) > 0
}

// FetchConfig validates the current cookies against Google and loads the
// device config. It is the first network call after cookie ingestion.
func (c *Client) FetchConfig(ctx context.Context) error {
	if c == nil || c.libgmClient == nil {
		return errors.New("gm client is not initialized")
	}
	return c.libgmClient.FetchConfig(ctx)
}

// SaveSession persists the current auth data to the session path atomically.
func (c *Client) SaveSession() error {
	if c == nil || c.authData == nil {
		return errors.New("gm client is not initialized")
	}
	return SaveAuthData(c.sessionPath, c.authData)
}

// PairWithCookies validates Google session cookies, persists them, and then
// starts the Gaia emoji challenge to link the phone. This mirrors the upstream
// mautrix-gmessages cookie login: cookies first, then emoji device pairing.
func (c *Client) PairWithCookies(ctx context.Context, cookies map[string]string, onEmoji func(string)) error {
	if len(cookies) == 0 {
		return errors.New("no cookies provided")
	}
	c.SetCookies(cookies)
	if err := c.FetchConfig(ctx); err != nil {
		return fmt.Errorf("cookies were rejected by Google (check that you copied a signed-in request from messages.google.com): %w", err)
	}
	if err := c.SaveSession(); err != nil {
		return fmt.Errorf("failed to persist cookies: %w", err)
	}
	return c.PairGaia(ctx, onEmoji)
}
