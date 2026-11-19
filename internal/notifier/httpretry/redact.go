package httpretry

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// redactedURL replaces a URL whose path, query, or userinfo could not be parsed.
const redactedURL = "[redacted]"

// RequestError is a *url.Error with the request URL cut down to scheme://host.
// Webhook paths and queries often carry tokens (Discord /api/webhooks/<id>/<token>,
// Slack /services/..., ?token=), and url.Error.Error() prints the full URL.
type RequestError struct {
	Op  string
	URL string // scheme://host only
	Err error
}

// Error leaves the URL unquoted, unlike url.Error: it is only scheme://host or
// [redacted] (no spaces or quotes), and a %q here would be quoted again by the
// log handler, giving error="Post \"https://…\": …".
func (e *RequestError) Error() string {
	return fmt.Sprintf("%s %s: %s", e.Op, e.URL, e.Err)
}

func (e *RequestError) Unwrap() error { return e.Err }

// Timeout mirrors url.Error.Timeout.
func (e *RequestError) Timeout() bool {
	t, ok := e.Err.(interface{ Timeout() bool })
	return ok && t.Timeout()
}

// Temporary mirrors url.Error.Temporary.
func (e *RequestError) Temporary() bool {
	t, ok := e.Err.(interface{ Temporary() bool })
	return ok && t.Temporary()
}

// redactedError keeps a rewritten message for wrappers around a *url.Error
// (e.g. "create request: parse \"…\"") while unwrapping to the redacted cause.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// RedactURLError strips path, query, fragment, and userinfo from any *url.Error
// in err's chain. errors.Is / errors.As on the cause (context.Canceled,
// net.Error, …) keep working. Errors without a *url.Error are returned as-is.
func RedactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	safe := &RequestError{Op: ue.Op, URL: endpointOnly(ue.URL), Err: RedactURLError(ue.Err)}
	if err == error(ue) {
		return safe
	}
	msg := strings.ReplaceAll(err.Error(), ue.Error(), safe.Error())
	if ue.URL != "" {
		msg = strings.ReplaceAll(msg, ue.URL, safe.URL)
	}
	return &redactedError{msg: msg, err: safe}
}

func endpointOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return redactedURL
	}
	return u.Scheme + "://" + u.Host
}
