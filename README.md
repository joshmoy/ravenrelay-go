# RavenRelay Go SDK

Go 1.22+, standard library only. Module: `github.com/joshmoy/ravenrelay-go`.

```sh
go get github.com/joshmoy/ravenrelay-go@v0.1.1
```

Import the client with `import "github.com/joshmoy/ravenrelay-go"`.

```go
client, err := ravenrelay.New(os.Getenv("RAVENRELAY_API_KEY"), ravenrelay.Options{
    BaseURL: os.Getenv("RAVENRELAY_API_URL"),
})
if err != nil { return err }
result, err := client.Emails.Send(ctx, ravenrelay.Email{
    "from": "hello@example.com", "to": "user@example.com",
    "subject": "Welcome", "text": "Hello",
}, "welcome-123")
if err != nil { return err }
email, err := client.Emails.Get(ctx, result["id"].(string))
page, err := client.Emails.List(ctx, ravenrelay.ListOptions{Status: "sent"})
```

Use `template`/`variables` instead of subject/body for saved project templates. Responses are maps preserving API fields. `APIError` exposes `Status`/`Code`; `TransportError` wraps the underlying error (including context cancellation) and means send acceptance may be uncertain. `ProtocolError` indicates an invalid successful response. No automatic retries; reuse the same idempotency key and body to retry a send. The default transport disables connection reuse to prevent implicit net/http retries. Custom transports must preserve this policy. Context cancellation and a total ten-second default timeout are supported; redirects are refused. Keep keys server-side. Default origin: `https://api.getravenrelay.com`; supply your deployed origin explicitly.

`VerifyWebhookSignature(secret, timestamp, signature, originalBytes, time.Time{})` checks HMAC-SHA256 with constant-time comparison and a five-minute tolerance. Verify before parsing JSON; durably deduplicate event IDs.

```sh
python3 testdata/contract_server.py testdata/run-go.sh
```

Release from the public SDK repository by tagging `v0.1.1`; Go modules use repository tags instead of an uploaded package. No reuse license has been granted.

Security: remote origins must use HTTPS. Plain HTTP is accepted only for literal `localhost`, `127.0.0.1` and IPv6 loopback hosts. Credentials/query/fragment, whitespace and backslashes are rejected in API origins. Authentication/idempotency headers must be printable ASCII; `.` and `..` email IDs are rejected. Response bodies are limited to 16 MiB, valid UTF-8 and 64 JSON nesting levels. TLS certificate verification stays enabled. Use vendor-supported runtimes with current security patches; the compatibility floor does not promise that an old runtime is secure. No third-party Go modules are required.
