// Package ravenrelay is a server-side HTTP client for RavenRelay. Sends are never automatically retried.
package ravenrelay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const Version = "0.1.0"
const maxResponse = 16 * 1024 * 1024

// Email and Response preserve the public API's JSON fields, including future additions.
type Email map[string]any
type Response map[string]any
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("RavenRelay %d (%s): %s", e.Status, e.Code, e.Message)
}

type TransportError struct{ Cause error }

func (e *TransportError) Error() string {
	return "RavenRelay network request failed; acceptance may be uncertain"
}
func (e *TransportError) Unwrap() error { return e.Cause }

type ProtocolError struct{ Message string }

func (e *ProtocolError) Error() string { return e.Message }

type Options struct {
	BaseURL string
	// Timeout is a total per-request deadline. Zero selects the ten-second default.
	Timeout time.Duration
	// Transport is optional; custom transports must not retry sends or follow redirects.
	Transport http.RoundTripper
}
type Client struct {
	key, baseURL string
	http         *http.Client
	Emails       *EmailsService
}
type EmailsService struct{ client *Client }
type ListOptions struct{ Status, Recipient, Cursor string }

func validHeader(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}
func New(apiKey string, options Options) (*Client, error) {
	if !validHeader(apiKey) {
		return nil, errors.New("apiKey must be a nonempty header value")
	}
	base := options.BaseURL
	if base == "" {
		base = "https://api.ravenrelay.com"
	}
	if strings.ContainsAny(base, "\\ \t\r\n\x00#") {
		return nil, errors.New("invalid BaseURL")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("BaseURL must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return nil, errors.New("HTTP is only allowed on literal loopback hosts; use HTTPS")
	}
	timeout := options.Timeout
	if timeout < 0 {
		return nil, errors.New("Timeout cannot be negative")
	}
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	transport := options.Transport
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		// net/http may retry idempotency-key requests on reused connections. Avoid that ambiguity.
		t.DisableKeepAlives = true
		transport = t
	}
	c := &Client{key: apiKey, baseURL: strings.TrimRight(base, "/"), http: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	c.Emails = &EmailsService{client: c}
	return c, nil
}
func (c *Client) request(ctx context.Context, method, path string, body Email, idempotencyKey string) (Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		if !validHeader(idempotencyKey) {
			return nil, errors.New("invalid idempotency key")
		}
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, &TransportError{Cause: err}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, &TransportError{Cause: err}
	}
	if len(raw) > maxResponse {
		return nil, &ProtocolError{Message: "RavenRelay response exceeds 16 MiB"}
	}
	if !utf8.Valid(raw) || excessiveDepth(raw) {
		return nil, &ProtocolError{Message: "Invalid UTF-8 or JSON nesting exceeds 64 levels"}
	}
	var parsed Response
	parseErr := json.Unmarshal(raw, &parsed)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		code, message := "unknown_error", "Request failed"
		if e, ok := parsed["error"].(map[string]any); ok {
			if v, ok := e["code"].(string); ok {
				code = v
			}
			if v, ok := e["message"].(string); ok {
				message = v
			}
		}
		return nil, &APIError{Status: res.StatusCode, Code: code, Message: message}
	}
	if parseErr != nil || parsed == nil {
		return nil, &ProtocolError{Message: "RavenRelay response must be a JSON object"}
	}
	return parsed, nil
}
func (e *EmailsService) Send(ctx context.Context, email Email, idempotencyKey string) (Response, error) {
	if email == nil {
		return nil, errors.New("email must be nonnil")
	}
	return e.client.request(ctx, http.MethodPost, "/v1/emails", email, idempotencyKey)
}
func (e *EmailsService) Get(ctx context.Context, id string) (Response, error) {
	if id == "" || id == "." || id == ".." {
		return nil, errors.New("email ID must be nonempty")
	}
	return e.client.request(ctx, http.MethodGet, "/v1/emails/"+strings.ReplaceAll(url.PathEscape(id), "+", "%2B"), nil, "")
}
func (e *EmailsService) List(ctx context.Context, options ListOptions) (Response, error) {
	values := url.Values{}
	for k, v := range map[string]string{"status": options.Status, "recipient": options.Recipient, "cursor": options.Cursor} {
		if v != "" {
			values.Set(k, v)
		}
	}
	path := "/v1/emails"
	if query := values.Encode(); query != "" {
		path += "?" + query
	}
	return e.client.request(ctx, http.MethodGet, path, nil, "")
}

var timestampPattern = regexp.MustCompile(`^[0-9]{1,12}$`)
var signaturePattern = regexp.MustCompile(`^v1=[a-f0-9]{64}$`)

// VerifyWebhookSignature checks the original body bytes. Also deduplicate event IDs.
// Pass time.Time{} to use the current time; timestamps are Unix seconds.
func VerifyWebhookSignature(secret, timestamp, signature string, body []byte, now time.Time) bool {
	if secret == "" || !timestampPattern.MatchString(timestamp) || !signaturePattern.MatchString(signature) {
		return false
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	delta := now.Sub(time.Unix(seconds, 0))
	if delta > 5*time.Minute || delta < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	received, err := hex.DecodeString(signature[3:])
	return err == nil && hmac.Equal(mac.Sum(nil), received)
}

func excessiveDepth(raw []byte) bool {
	depth := 0
	quoted, escaped := false, false
	for _, c := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
		} else if c == '"' {
			quoted = true
		} else if c == '{' || c == '[' {
			depth++
			if depth > 64 {
				return true
			}
		} else if c == '}' || c == ']' {
			depth--
		}
	}
	return false
}
