# SMTPfast Go SDK

The official Go SDK for the [SMTPfast](https://smtpfa.st) API. It covers every endpoint in the API, with a struct for every request and response, and uses only the standard library. It needs Go 1.24+.

```bash
go get github.com/smtpfast/smtpfast-go
```

## Send an email

```go
client := smtpfast.NewClient() // reads SMTPFAST_API_KEY

email, err := client.Emails.Send(ctx, &smtpfast.SendEmailRequest{
	From:     smtpfast.String("Acme <hello@yourdomain.com>"),
	To:       smtpfast.StringList{"customer@example.org"},
	Subject:  smtpfast.String("Your receipt"),
	HTML:     smtpfast.String("<p>Thanks for your order.</p>"),
	Category: smtpfast.String("transactional"),
})
if err != nil {
	return err
}
fmt.Println(email.ID)
```

Email is marketing unless you set `category` to `transactional`. Marketing email gets a one-click unsubscribe header and is not sent to people who unsubscribed. Set `transactional` on receipts, password resets and account alerts so a marketing opt-out does not block them.

Every API operation is a method, named after its path: `client.Domains.Verify(ctx, id)`, `client.Contacts.Segments.Add(ctx, contactID, segmentID)`, `client.Inboxes.Threads.Emails.Reply(ctx, inboxID, threadID, emailID, params)`. Path parameters come first, then the request body, then the query struct, then options. Optional fields are pointers; `smtpfast.String`, `smtpfast.Int` and `smtpfast.Bool` make them. A field that is optional and can be null is a `smtpfast.Nullable[T]`: leave it out, set `smtpfast.Value(x)`, or send `smtpfast.Null[T]()` to clear it. An empty list that you set is sent, so it clears the list.

## Options

```go
client := smtpfast.NewClient(
	smtpfast.WithAPIKey("sf_..."),
	smtpfast.WithMaxRetries(2),            // after 429 (with Retry-After), 408, 5xx and dropped connections
	smtpfast.WithTimeout(60*time.Second),  // per attempt
)

client.Emails.Send(ctx, params, smtpfast.WithIdempotencyKey("order-1234"), smtpfast.WithRequestTimeout(10*time.Second))
```

Reads, `PUT` and `DELETE` are retried after a 5xx answer or a dropped connection. A send, reply or forward is retried with the same `Idempotency-Key`, which the SDK makes when you do not give one, so the API returns the first answer instead of sending twice. Other `POST` and `PATCH` calls are retried only after a per-second rate limit.

## Errors

An answer outside 2xx is an `*smtpfast.APIError` with `StatusCode`, `Message` and the raw `Body`. No answer is a `*smtpfast.ConnectionError`. `smtpfast.IsNotFound(err)`, `IsUnauthorized`, `IsForbidden` and `IsRateLimited` check the common cases.

## Pagination

Lists that page with a cursor also have an `...All` method that returns an iterator:

```go
for email, err := range client.Emails.ListAll(ctx, &smtpfast.ListEmailsQuery{Limit: smtpfast.Int(100)}) {
	if err != nil {
		return err
	}
	fmt.Println(email.ID, email.Status)
}
```

## Webhooks

```go
body, _ := io.ReadAll(r.Body) // the raw body, before any JSON parsing
if !smtpfast.VerifyWebhookSignature(body, r.Header.Get("X-SMTPfast-Signature"), secret) {
	http.Error(w, "bad signature", http.StatusUnauthorized)
	return
}
```

## How this SDK is made

`scripts/generate.py` (Python 3 standard library) writes `types_gen.go` and `resources_gen.go` from [spec/openapi.json](https://smtpfa.st/api/v1/openapi.json), naming every method from the `x-sdk-group` and `x-sdk-method` the spec gives each operation; the SDKs for other languages use the same names. `client.go`, `core.go`, `errors.go` and `webhooks.go` are written by hand.

```bash
python3 scripts/pull_spec.py   # download the published spec
python3 scripts/generate.py    # regenerate types_gen.go and resources_gen.go
go test ./...                  # unit tests, including one call per operation
SMTPFAST_API_KEY=sf_... go test -run TestLive -v ./...   # read-only calls; every answer must decode
```

## License

MIT
