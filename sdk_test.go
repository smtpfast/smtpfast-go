package smtpfast

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

type recorded struct {
	method, path, query string
	header              http.Header
	body                string
}

type reply struct {
	status int
	body   string
	header map[string]string
	err    error
}

type recorder struct {
	calls   []recorded
	replies []reply
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	b := ""
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		b = string(data)
	}
	r.calls = append(r.calls, recorded{req.Method, req.URL.EscapedPath(), req.URL.RawQuery, req.Header.Clone(), b})
	rep := reply{status: 200, body: `{}`}
	if len(r.replies) > 0 {
		rep, r.replies = r.replies[0], r.replies[1:]
	}
	if rep.err != nil {
		return nil, rep.err
	}
	h := http.Header{"Content-Type": {"application/json"}}
	for k, v := range rep.header {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: rep.status, Header: h, Body: io.NopCloser(strings.NewReader(rep.body)), Request: req}, nil
}

func newTestClient(replies ...reply) (*Client, *recorder) {
	rec := &recorder{replies: replies}
	sleep = func(context.Context, time.Duration) error { return nil }
	return NewClient(WithAPIKey("sf_test_key"), WithHTTPClient(&http.Client{Transport: rec})), rec
}

var ctx = context.Background()

func TestSend(t *testing.T) {
	c, rec := newTestClient(reply{status: 200, body: `{"id":"em_1","object":"email"}`})
	email, err := c.Emails.Send(ctx, &SendEmailRequest{From: String("a@yourapp.dev"), To: StringList{"b@inbox.dev"}, Subject: String("Hi"), Text: String("Hello")})
	if err != nil || email.ID != "em_1" {
		t.Fatalf("got %+v, %v", email, err)
	}
	call := rec.calls[0]
	if call.method != "POST" || call.path != "/api/v1/emails" {
		t.Fatalf("sent %s %s", call.method, call.path)
	}
	if call.header.Get("Authorization") != "Bearer sf_test_key" || !strings.HasPrefix(call.header.Get("User-Agent"), "smtpfast-go/") {
		t.Fatalf("headers %v", call.header)
	}
	if call.body != `{"from":"a@yourapp.dev","to":["b@inbox.dev"],"subject":"Hi","text":"Hello"}` {
		t.Fatalf("body %s", call.body)
	}
}

func TestPathAndQuery(t *testing.T) {
	c, rec := newTestClient()
	if _, err := c.Inboxes.Threads.Emails.Reply(ctx, "in 1", "th_2", "em/3", &ReplyToInboxThreadEmailParams{Text: String("Thanks")}); err != nil {
		t.Fatal(err)
	}
	if rec.calls[0].path != "/api/v1/inboxes/in%201/threads/th_2/emails/em%2F3/reply" {
		t.Fatalf("path %s", rec.calls[0].path)
	}
	c, rec = newTestClient(reply{status: 200, body: `{"data":[],"has_more":false}`})
	if _, err := c.Emails.List(ctx, &ListEmailsQuery{Limit: Int(5), After: String("em_9")}); err != nil {
		t.Fatal(err)
	}
	if rec.calls[0].query != "after=em_9&limit=5" {
		t.Fatalf("query %s", rec.calls[0].query)
	}
}

func TestErrors(t *testing.T) {
	c, _ := newTestClient(reply{status: 404, body: `{"error":"Email not found"}`})
	_, err := c.Emails.Get(ctx, "em_x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 || apiErr.Message != "Email not found" || !IsNotFound(err) {
		t.Fatalf("got %v", err)
	}
}

func TestRetries(t *testing.T) {
	noWait := map[string]string{"Retry-After": "0"}
	c, rec := newTestClient(reply{status: 503, body: `{}`, header: noWait}, reply{status: 200, body: `[]`})
	if _, err := c.Domains.List(ctx); err != nil || len(rec.calls) != 2 {
		t.Fatalf("GET after 503: %v, %d calls", err, len(rec.calls))
	}
	c, rec = newTestClient(reply{status: 500, body: `{}`, header: noWait}, reply{status: 200, body: `{"id":"em_1"}`})
	if _, err := c.Emails.Send(ctx, &SendEmailRequest{To: StringList{"b@inbox.dev"}}); err != nil {
		t.Fatal(err)
	}
	k1, k2 := rec.calls[0].header.Get("Idempotency-Key"), rec.calls[1].header.Get("Idempotency-Key")
	if k1 == "" || k1 != k2 {
		t.Fatalf("keys %q %q", k1, k2)
	}
	c, rec = newTestClient(reply{status: 500, body: `{}`, header: noWait})
	if _, err := c.Contacts.Create(ctx, &CreateContactRequest{Email: "b@inbox.dev"}, WithHeader("Idempotency-Key", "x")); err == nil || len(rec.calls) != 1 {
		t.Fatalf("POST without dedupe was retried: %d calls", len(rec.calls))
	}
	c, rec = newTestClient(reply{status: 429, body: `{}`})
	if _, err := c.Domains.List(ctx); !IsRateLimited(err) || len(rec.calls) != 1 {
		t.Fatalf("429 without Retry-After: %v, %d calls", err, len(rec.calls))
	}
	c, rec = newTestClient(reply{status: 503, header: map[string]string{"Retry-After": "120"}})
	if _, err := c.Domains.List(ctx); err == nil || len(rec.calls) != 1 {
		t.Fatalf("long Retry-After: %d calls", len(rec.calls))
	}
	drop := errors.New("connection reset")
	c, rec = newTestClient(reply{err: drop}, reply{err: drop}, reply{err: drop})
	var connErr *ConnectionError
	if _, err := c.Domains.List(ctx, WithRequestMaxRetries(1)); !errors.As(err, &connErr) || len(rec.calls) != 2 {
		t.Fatalf("dropped: %v, %d calls", err, len(rec.calls))
	}
}

func TestListAll(t *testing.T) {
	c, rec := newTestClient(
		reply{status: 200, body: `{"object":"list","data":[{"id":"a"},{"id":"b"}],"has_more":true}`},
		reply{status: 200, body: `{"object":"list","data":[{"id":"c"}],"has_more":false}`},
	)
	var ids []string
	for item, err := range c.Logs.ListAll(ctx, &ListLogsQuery{Limit: Int(2), Before: String("x")}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, *item.ID)
	}
	if strings.Join(ids, ",") != "a,b,c" || rec.calls[0].query != "limit=2" || rec.calls[1].query != "after=b&limit=2" {
		t.Fatalf("ids %v, queries %q %q", ids, rec.calls[0].query, rec.calls[1].query)
	}
}

func TestWebhookSignature(t *testing.T) {
	payload := []byte(`{"type":"email.delivered"}`)
	mac := hmac.New(sha256.New, []byte("whsec"))
	mac.Write(payload)
	sig := hex.EncodeToString(mac.Sum(nil))
	if !VerifyWebhookSignature(payload, sig, "whsec") || VerifyWebhookSignature(append(payload, ' '), sig, "whsec") || VerifyWebhookSignature(payload, "é", "whsec") {
		t.Fatal("signature check")
	}
}

// Every operation in the spec is reachable as client.<Group>.<Method> and
// sends the method and path the spec documents.
func TestEveryOperation(t *testing.T) {
	raw, err := os.ReadFile("spec/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Group  []string `json:"x-sdk-group"`
			Method string   `json:"x-sdk-method"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	param := regexp.MustCompile(`\{\w+\}`)
	count := 0
	for path, item := range spec.Paths {
		for method, op := range item {
			if op.Method == "" {
				continue
			}
			count++
			c, rec := newTestClient()
			target := reflect.ValueOf(c.Service)
			for _, g := range op.Group {
				target = target.Elem().FieldByName(goName(g))
				if !target.IsValid() {
					break
				}
			}
			if !target.IsValid() {
				t.Errorf("group %s missing", strings.Join(op.Group, "."))
				continue
			}
			fn := target.MethodByName(goName(op.Method))
			if !fn.IsValid() {
				t.Errorf("%s.%s missing", strings.Join(op.Group, "."), op.Method)
				continue
			}
			args := []reflect.Value{reflect.ValueOf(ctx)}
			for i := 1; i < fn.Type().NumIn(); i++ {
				in := fn.Type().In(i)
				switch {
				case fn.Type().IsVariadic() && i == fn.Type().NumIn()-1:
				case in.Kind() == reflect.String:
					args = append(args, reflect.ValueOf("p"+string(rune('0'+len(args)-1))))
				default:
					args = append(args, reflect.Zero(in))
				}
			}
			fn.Call(args)
			n := 0
			want := "/api" + param.ReplaceAllStringFunc(path, func(string) string { n++; return "p" + string(rune('0'+n-1)) })
			if len(rec.calls) != 1 || rec.calls[0].method != strings.ToUpper(method) || rec.calls[0].path != want {
				t.Errorf("%s %s: sent %+v", method, path, rec.calls)
			}
		}
	}
	if count == 0 {
		t.Fatal("no operations in the spec")
	}
}

// goName mirrors go_name in scripts/generate.py.
func goName(s string) string {
	initialisms := map[string]string{"id": "ID", "ids": "IDs", "url": "URL", "urls": "URLs", "api": "API", "html": "HTML", "dns": "DNS", "ip": "IP", "http": "HTTP", "json": "JSON", "dkim": "DKIM", "spf": "SPF", "dmarc": "DMARC", "mx": "MX", "smtp": "SMTP", "ai": "AI"}
	var out strings.Builder
	for _, w := range regexp.MustCompile(`[A-Z]?[a-z0-9]+|[A-Z]+`).FindAllString(s, -1) {
		if v, ok := initialisms[strings.ToLower(w)]; ok {
			out.WriteString(v)
		} else {
			out.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return out.String()
}

func TestReviewFixes(t *testing.T) {
	// A key on an operation that does not dedupe is dropped, so net/http cannot replay the POST.
	c, rec := newTestClient()
	if _, err := c.Contacts.Create(ctx, &CreateContactRequest{Email: "b@inbox.dev"}, WithHeader("Idempotency-Key", "x")); err != nil {
		t.Fatal(err)
	}
	if rec.calls[0].header.Get("Idempotency-Key") != "" {
		t.Fatal("Idempotency-Key sent on an operation that does not dedupe")
	}
	// Absent, null and an empty list are three different requests.
	c, rec = newTestClient()
	if _, err := c.Inboxes.Update(ctx, "in_1", &UpdateInboxParams{FromName: Null[string]()}); err != nil {
		t.Fatal(err)
	}
	if rec.calls[0].body != `{"from_name":null}` {
		t.Fatalf("null body %s", rec.calls[0].body)
	}
	c, rec = newTestClient()
	if _, err := c.Contacts.Update(ctx, "ct_1", &UpdateContactRequest{SegmentIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if rec.calls[0].body != `{"segment_ids":[]}` {
		t.Fatalf("empty list body %s", rec.calls[0].body)
	}
	// The typed export asks for JSON.
	c, rec = newTestClient(reply{status: 200, body: `{"data":[]}`})
	if _, err := c.Contacts.Export(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.calls[0].query, "format=json") {
		t.Fatalf("export query %q", rec.calls[0].query)
	}
	// A huge Retry-After stops the retries instead of overflowing.
	c, rec = newTestClient(reply{status: 503, header: map[string]string{"Retry-After": "10000000000"}})
	if _, err := c.Domains.List(ctx); err == nil || len(rec.calls) != 1 {
		t.Fatalf("huge Retry-After: %d calls", len(rec.calls))
	}
	// Ranging twice over one iterator starts from the first page both times.
	c, rec = newTestClient(
		reply{status: 200, body: `{"data":[{"id":"a"}],"has_more":true}`}, reply{status: 200, body: `{"data":[],"has_more":false}`},
		reply{status: 200, body: `{"data":[{"id":"a"}],"has_more":true}`}, reply{status: 200, body: `{"data":[],"has_more":false}`},
	)
	seq := c.Logs.ListAll(ctx, nil)
	for range 2 {
		for _, err := range seq {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if rec.calls[2].query != "" {
		t.Fatalf("second range started at %q", rec.calls[2].query)
	}
}
