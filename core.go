package smtpfast

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type core struct {
	apiKey     string
	baseURL    string
	maxRetries int
	timeout    time.Duration
	http       *http.Client
}

// RequestOption changes one call.
type RequestOption func(*requestSettings)

type requestSettings struct {
	idempotencyKey string
	maxRetries     *int
	timeout        *time.Duration
	headers        http.Header
}

// WithIdempotencyKey sets the Idempotency-Key of a send, reply or forward.
// A retry with the same key and body returns the first answer instead of
// sending again. Without it, the SDK makes one per call when retries are on.
func WithIdempotencyKey(key string) RequestOption {
	return func(s *requestSettings) { s.idempotencyKey = key }
}

// WithRequestMaxRetries overrides the client's retry count for one call.
func WithRequestMaxRetries(n int) RequestOption {
	return func(s *requestSettings) { s.maxRetries = &n }
}

// WithRequestTimeout overrides the client's per-attempt timeout for one call.
func WithRequestTimeout(d time.Duration) RequestOption {
	return func(s *requestSettings) { s.timeout = &d }
}

// WithHeader adds a header to one call.
func WithHeader(key, value string) RequestOption {
	return func(s *requestSettings) { s.headers.Set(key, value) }
}

var retryStatus = map[int]bool{408: true, 429: true, 500: true, 502: true, 503: true, 504: true}

const maxRetryAfter = 60 * time.Second

// paginationManaged are query parameters a ...All iterator sets, or that cannot combine with walking forward by after.
var paginationManaged = []string{"before", "page", "latest"}

func (c *core) do(ctx context.Context, method, route string, pathParams map[string]string, query url.Values, body any, idempotent bool, out any, opts []RequestOption) error {
	if c.apiKey == "" {
		return errors.New("smtpfast: no API key: use WithAPIKey or set SMTPFAST_API_KEY")
	}
	settings := requestSettings{headers: http.Header{}}
	for _, opt := range opts {
		opt(&settings)
	}
	maxRetries, timeout := c.maxRetries, c.timeout
	if settings.maxRetries != nil {
		maxRetries = *settings.maxRetries
	}
	if settings.timeout != nil {
		timeout = *settings.timeout
	}
	for name, value := range pathParams {
		route = strings.ReplaceAll(route, "{"+name+"}", url.PathEscape(value))
	}
	target := c.baseURL + route
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("smtpfast: encode request: %w", err)
		}
	}
	key := settings.idempotencyKey
	if key == "" && idempotent && settings.headers.Get("Idempotency-Key") == "" && maxRetries > 0 {
		key = newUUID()
	}
	if key == "" && idempotent {
		key = settings.headers.Get("Idempotency-Key")
	}
	// A POST or PATCH is retried only when its operation dedupes on the key.
	retryable := method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete || (idempotent && key != "")

	for attempt := 0; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(attemptCtx, method, target, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("User-Agent", "smtpfast-go/"+Version)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range settings.headers {
			req.Header[k] = v
		}
		// net/http replays a request that carries Idempotency-Key after a broken
		// connection, so the header goes only on operations that dedupe on it.
		req.Header.Del("Idempotency-Key")
		req.Header.Del("X-Idempotency-Key")
		if idempotent && key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		res, err := c.http.Do(req)
		var data []byte
		if err == nil {
			data, err = io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil && retryStatus[res.StatusCode] {
				// The status line arrived, so its retry rules apply, not the connection rules.
				cancel()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				wait, ok := retryDelay(res, attempt, retryable)
				if !ok || attempt >= maxRetries {
					return &ConnectionError{Err: err}
				}
				if err := sleep(ctx, wait); err != nil {
					return err
				}
				continue
			}
		}
		cancel()
		if err != nil {
			// No answer, or the body broke off: the same retry rules apply.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			connErr := &ConnectionError{Err: err, Timeout: errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded)}
			if !retryable || attempt >= maxRetries {
				return connErr
			}
			if err := sleep(ctx, backoff(attempt)); err != nil {
				return err
			}
			continue
		}
		if wait, ok := retryDelay(res, attempt, retryable); ok && attempt < maxRetries {
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return newAPIError(res, data)
		}
		if out == nil || len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("smtpfast: decode %s %s response: %w", method, route, err)
		}
		return nil
	}
}

func retryDelay(res *http.Response, attempt int, retryable bool) (time.Duration, bool) {
	if !retryStatus[res.StatusCode] {
		return 0, false
	}
	after, hasAfter := parseRetryAfter(res.Header.Get("Retry-After"))
	if res.StatusCode == 429 {
		// A per-second limit sends Retry-After and rejects before any work is
		// done, so any method may retry. Without the header (an hourly or daily
		// limit), retrying soon cannot succeed.
		return after, hasAfter && after <= maxRetryAfter
	}
	if !retryable {
		return 0, false
	}
	if hasAfter {
		return after, after <= maxRetryAfter
	}
	return backoff(attempt), true
}

func parseRetryAfter(h string) (time.Duration, bool) {
	if h == "" {
		return 0, false
	}
	if s, err := strconv.ParseFloat(h, 64); err == nil {
		if math.IsNaN(s) || math.IsInf(s, 0) {
			return 0, false
		}
		if s > maxRetryAfter.Seconds() {
			return maxRetryAfter + time.Second, true // too long to wait; checked before converting, so it cannot overflow
		}
		return time.Duration(math.Max(s, 0) * float64(time.Second)), true
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(time.Until(t), 0), true
	}
	return 0, false
}

func backoff(attempt int) time.Duration {
	base := math.Min(0.5*math.Pow(2, float64(attempt)), 8)
	return time.Duration(base * (0.75 + mrand.Float64()*0.5) * float64(time.Second))
}

var sleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func setQuery(q url.Values, name string, value any) {
	switch v := value.(type) {
	case nil:
	case []string:
		for _, s := range v {
			q.Add(name, s)
		}
	case StringList:
		for _, s := range v {
			q.Add(name, s)
		}
	default:
		q.Set(name, fmt.Sprint(v))
	}
}

func paginate[T any](ctx context.Context, c *core, route string, pathParams map[string]string, q url.Values, opts []RequestOption) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		// Each range over the iterator starts from the first page.
		q := cloneValues(q)
		for _, k := range paginationManaged {
			q.Del(k)
		}
		for {
			var page struct {
				Data    []json.RawMessage `json:"data"`
				HasMore bool              `json:"has_more"`
			}
			if err := c.do(ctx, http.MethodGet, route, pathParams, q, nil, false, &page, opts); err != nil {
				var zero T
				yield(zero, err)
				return
			}
			var lastID string
			for _, raw := range page.Data {
				var item T
				if err := json.Unmarshal(raw, &item); err != nil {
					var zero T
					yield(zero, err)
					return
				}
				var id struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal(raw, &id)
				lastID = id.ID
				if !yield(item, nil) {
					return
				}
			}
			if !page.HasMore || lastID == "" {
				return
			}
			q.Set("after", lastID)
		}
	}
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

// Nullable is a field that can be left out, set to null, or set to a value.
// A request leaves it out by default; Null clears the field, Value sets it.
type Nullable[T any] struct {
	Value T
	// Valid is false for null.
	Valid bool
	// Set is false when the field is left out (or was absent in a response).
	Set bool
}

// Null returns a Nullable that sends null, which clears the field.
func Null[T any]() Nullable[T] { return Nullable[T]{Set: true} }

// Value returns a Nullable that sends v.
func Value[T any](v T) Nullable[T] { return Nullable[T]{Value: v, Valid: true, Set: true} }

// IsZero reports whether the field is left out, for the omitzero tag.
func (n Nullable[T]) IsZero() bool { return !n.Set }

// MarshalJSON writes null or the value.
func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Value)
}

// UnmarshalJSON reads null or a value.
func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(bytes.TrimSpace(b)) == "null" {
		n.Valid = false
		return nil
	}
	n.Valid = true
	return json.Unmarshal(b, &n.Value)
}

// StringList is a list of strings that also reads a single JSON string, for
// fields such as to and cc that take one address or several.
type StringList []string

// UnmarshalJSON accepts a string or an array of strings.
func (l *StringList) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*l = StringList{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*l = list
	return nil
}
