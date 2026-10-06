package smtpfast

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// APIError is an answer outside 2xx. Check the status with errors.As, or
// with IsNotFound, IsRateLimited and the other helpers.
type APIError struct {
	StatusCode int
	// Message is the API's "error" field, or the raw body when it is not JSON.
	Message string
	// Body is the raw response body; it can hold more fields, such as missing_variables.
	Body   []byte
	Header http.Header
}

func (e *APIError) Error() string { return fmt.Sprintf("smtpfast: %d: %s", e.StatusCode, e.Message) }

func newAPIError(res *http.Response, body []byte) *APIError {
	e := &APIError{StatusCode: res.StatusCode, Body: body, Header: res.Header}
	var parsed struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != "" {
		e.Message = parsed.Error
	} else if s := strings.TrimSpace(string(body)); s != "" {
		e.Message = s[:min(len(s), 300)]
	} else {
		e.Message = http.StatusText(res.StatusCode)
	}
	return e
}

// ConnectionError means no answer arrived: DNS, TLS, a reset connection, or a timeout.
type ConnectionError struct {
	Err     error
	Timeout bool
}

func (e *ConnectionError) Error() string {
	if e.Timeout {
		return "smtpfast: the API did not answer in time: " + e.Err.Error()
	}
	return "smtpfast: could not connect to the API: " + e.Err.Error()
}

func (e *ConnectionError) Unwrap() error { return e.Err }

func statusIs(err error, code int) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == code
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool { return statusIs(err, 404) }

// IsUnauthorized reports whether err is a 401: a missing or wrong API key.
func IsUnauthorized(err error) bool { return statusIs(err, 401) }

// IsForbidden reports whether err is a 403: the key lacks the scope, or the domain is not verified.
func IsForbidden(err error) bool { return statusIs(err, 403) }

// IsRateLimited reports whether err is a 429.
func IsRateLimited(err error) bool { return statusIs(err, 429) }
