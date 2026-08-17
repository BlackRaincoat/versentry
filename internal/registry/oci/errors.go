package oci

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// sanitizeRegistryErr formats errors for logs without Go %q URL quoting
// (avoids err="Get \"https://…\": …").
func sanitizeRegistryErr(err error) string {
	if err == nil {
		return ""
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		cause := ue.Err
		if cause == nil {
			return fmt.Sprintf("%s %s", ue.Op, ue.URL)
		}
		return fmt.Sprintf("%s %s: %v", ue.Op, ue.URL, cause)
	}
	return err.Error()
}

// isResponseHeaderTimeout reports a stream-level wait for headers, not dial/TLS
// failure, 429, or parent cancel/deadline (SIGTERM).
func isResponseHeaderTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	// HTTP/1.1 header timeout implements Is(DeadlineExceeded); the string is
	// the stream-level class we evict. Bare deadline/cancel without that
	// string is SIGTERM or parent ctx — not eviction.
	return strings.Contains(err.Error(), "timeout awaiting response headers")
}
