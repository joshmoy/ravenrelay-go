package ravenrelay

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestContract(t *testing.T) {
	base := os.Getenv("RAVENRELAY_CONTRACT_URL")
	if base == "" {
		t.Skip("run through sdks/testing/run-go.sh")
	}
	ctx := context.Background()
	tls, err := New("rr_test_contract", Options{BaseURL: os.Getenv("RAVENRELAY_TLS_TEST_URL")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tls.Emails.Send(ctx, Email{"text": "test"}, "")
	var tlsError *TransportError
	if !errors.As(err, &tlsError) {
		t.Fatal("untrusted TLS certificate accepted", err)
	}
	client := func(name string, timeout time.Duration) *Client {
		c, err := New("rr_test_contract", Options{BaseURL: base + "/" + name + "/", Timeout: timeout})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	body := Email{"from": "sender@example.com", "to": []string{"a@example.com", "b@example.com"}, "subject": "Hello ☃", "text": "line one\nline two", "metadata": map[string]any{"number": 3, "enabled": true}, "tags": map[string]string{"kind": "welcome"}}
	check := func(r Response, err error, field string, value any) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if r[field] != value {
			t.Fatalf("%s = %v", field, r[field])
		}
	}
	r, err := client("send", 0).Emails.Send(ctx, body, "retry-123")
	check(r, err, "id", "email_123")
	r, err = client("template", 0).Emails.Send(ctx, Email{"from": "sender@example.com", "to": "a@example.com", "template": "welcome", "variables": map[string]any{"name": "Zoë", "count": 2, "active": true}}, "")
	check(r, err, "status", "queued")
	r, err = client("suppressed", 0).Emails.Send(ctx, body, "")
	check(r, err, "status", "suppressed")
	r, err = client("simulated", 0).Emails.Send(ctx, body, "")
	check(r, err, "test_mode", true)
	r, err = client("get", 0).Emails.Get(ctx, "email/id +?")
	check(r, err, "id", "email_123")
	r, err = client("list", 0).Emails.List(ctx, ListOptions{Status: "queued", Recipient: "a+tag@example.com", Cursor: "cur /?+"})
	check(r, err, "next_cursor", nil)
	r, err = client("empty-list", 0).Emails.List(ctx, ListOptions{})
	if err != nil || len(r["emails"].([]any)) != 0 {
		t.Fatal(r, err)
	}
	cases := []struct {
		name   string
		status int
		code   string
	}{{"unauthorized", 401, "invalid_api_key"}, {"validation", 422, "validation_error"}, {"rate", 429, "rate_limit_exceeded"}, {"server-error", 500, "internal_error"}, {"unknown-error", 503, "unknown_error"}, {"bad-error", 502, "unknown_error"}, {"non-json-error", 502, "unknown_error"}, {"redirect", 307, "unknown_error"}, {"bad-fields", 503, "unknown_error"}}
	for _, v := range cases {
		_, err := client(v.name, 0).Emails.Send(ctx, body, "")
		var api *APIError
		if !errors.As(err, &api) || api.Status != v.status || api.Code != v.code {
			t.Fatalf("%s: %v", v.name, err)
		}
	}
	for _, name := range []string{"malformed", "array", "null", "oversized", "deep-json", "invalid-utf8"} {
		_, err := client(name, 0).Emails.Send(ctx, body, "")
		var protocol *ProtocolError
		if !errors.As(err, &protocol) {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"timeout", "slow-body", "truncated", "disconnect", "drip-body"} {
		_, err = client(name, 100*time.Millisecond).Emails.Send(ctx, body, "")
		var transport *TransportError
		if !errors.As(err, &transport) {
			t.Fatal(name, err)
		}
	}
	for _, id := range []string{".", ".."} {
		if _, err := client("invalid", 0).Emails.Get(ctx, id); err == nil {
			t.Fatal("dot segment")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = client("canceled", 0).Emails.Send(canceled, body, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, key := range []string{"", "key\r\ninjected"} {
		if _, err := New(key, Options{}); err == nil {
			t.Fatal("invalid key")
		}
	}
	for _, url := range []string{"file:///etc/passwd", "https://user:pass@example.com", "https://example.com?key=secret", "http://example.com", "http://127.0.0.1.evil.test", "http://2130706433", "http://127.1", "http://localhost.evil.test", "https://example.com\\@evil.test", "https://example.com\n"} {
		if _, err := New("key", Options{BaseURL: url}); err == nil {
			t.Fatal("invalid URL")
		}
	}
	if _, err := New("key", Options{Timeout: -1}); err == nil {
		t.Fatal("invalid timeout")
	}
	if _, err := client("invalid", 0).Emails.Send(ctx, body, "x\nInjected: yes"); err == nil {
		t.Fatal("header injection")
	}
}
func TestWebhookVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/webhook-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name, Secret, Body   string
		Timestamp, Signature *string
		Now                  int64
		Valid                bool
	}
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			timestamp, signature := "", ""
			if v.Timestamp != nil {
				timestamp = *v.Timestamp
			}
			if v.Signature != nil {
				signature = *v.Signature
			}
			if VerifyWebhookSignature(v.Secret, timestamp, signature, []byte(v.Body), time.Unix(v.Now, 0)) != v.Valid {
				t.Fatal("signature result")
			}
		})
	}
}
