// Package smtpfast is the official Go SDK for the SMTPfast API.
//
//	client := smtpfast.NewClient() // reads SMTPFAST_API_KEY
//	email, err := client.Emails.Send(ctx, &smtpfast.SendEmailRequest{
//		From: smtpfast.String("you@yourdomain.com"), To: smtpfast.StringList{"them@example.org"},
//		Subject: smtpfast.String("Hi"), Text: smtpfast.String("Hello"),
//	})
package smtpfast

import (
	"net/http"
	"os"
	"strings"
	"time"
)

// Version is the SDK version, sent in the User-Agent header.
const Version = "0.1.1"

// Client is the SMTPfast API client. Every operation is a method on one of
// its services, named after the path: client.Emails.Send,
// client.Inboxes.Threads.Emails.Reply.
type Client struct {
	*Service
}

// Option configures a Client.
type Option func(*core)

// WithAPIKey sets the API key. The default is the SMTPFAST_API_KEY environment variable.
func WithAPIKey(key string) Option { return func(c *core) { c.apiKey = key } }

// WithBaseURL sets the API base URL. The default is https://smtpfa.st/api.
func WithBaseURL(u string) Option { return func(c *core) { c.baseURL = strings.TrimRight(u, "/") } }

// WithMaxRetries sets how many times a failed request is retried. The default is 2.
func WithMaxRetries(n int) Option { return func(c *core) { c.maxRetries = n } }

// WithTimeout sets the time limit for each attempt. The default is 60 seconds.
func WithTimeout(d time.Duration) Option { return func(c *core) { c.timeout = d } }

// WithHTTPClient sets the http.Client used for requests.
func WithHTTPClient(h *http.Client) Option { return func(c *core) { c.http = h } }

// NewClient makes a client. Without WithAPIKey it reads SMTPFAST_API_KEY;
// a request without a key fails with an error that says so.
func NewClient(opts ...Option) *Client {
	c := &core{
		apiKey:     strings.TrimSpace(os.Getenv("SMTPFAST_API_KEY")),
		baseURL:    "https://smtpfa.st/api",
		maxRetries: 2,
		timeout:    60 * time.Second,
		http:       http.DefaultClient,
	}
	if u := os.Getenv("SMTPFAST_BASE_URL"); u != "" {
		c.baseURL = strings.TrimRight(u, "/")
	}
	for _, opt := range opts {
		opt(c)
	}
	return &Client{Service: newService(c)}
}

// String returns a pointer to s, for optional fields.
func String(s string) *string { return &s }

// Int returns a pointer to n, for optional fields.
func Int(n int64) *int64 { return &n }

// Bool returns a pointer to b, for optional fields.
func Bool(b bool) *bool { return &b }
