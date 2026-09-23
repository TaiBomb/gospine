package svcclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/TaiBomb/gospine/logging"
	"github.com/danielgtaylor/huma/v2"
)

// Problem is the RFC 7807 document a gospine service fails with.
type Problem struct {
	Type   string `json:"type,omitempty"`
	Title  string `json:"title,omitempty"`
	Status int    `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// StatusError is a call the service answered outside 2xx.
type StatusError struct {
	StatusCode int
	Problem    Problem
	Body       string
}

func newStatusError(statusCode int, head []byte) *StatusError {
	e := &StatusError{StatusCode: statusCode, Body: string(head)}
	_ = json.Unmarshal(head, &e.Problem)

	return e
}

func (e *StatusError) Error() string {
	if e.Problem.Detail != "" {
		return fmt.Sprintf("service answered %d: %s", e.StatusCode, e.Problem.Detail)
	}

	return fmt.Sprintf("service answered %d", e.StatusCode)
}

// IsNotFound reports whether the service answered 404.
func IsNotFound(err error) bool {
	var statusErr *StatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound
}

// IsTimeout reports whether the call ran out of time, on either side: the
// client timeout or deadline, or a 504 from the service.
func IsTimeout(err error) bool {
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusGatewayTimeout
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// UpstreamSpec defines how a failed call to a service is reported.
type UpstreamSpec struct {
	LogMessage    string
	ClientMessage string

	// NotFoundMessage, when set, turns a 404 of the service into a 404.
	NotFoundMessage string
	// TimeoutMessage, when set, turns a timeout into a 504.
	TimeoutMessage string
}

// Upstream logs a failed call and maps it to an HTTP error: 404 or 504 when
// configured, 502 otherwise, never exposing what the service answered.
func Upstream(ctx context.Context, err error, spec UpstreamSpec, logFields ...any) error {
	log := logging.FromContext(ctx)

	if spec.NotFoundMessage != "" && IsNotFound(err) {
		log.Debug(spec.LogMessage+": not found upstream", logFields...)
		return huma.Error404NotFound(spec.NotFoundMessage)
	}

	log.Error(spec.LogMessage, ErrorLogFields(err, logFields...)...)

	if spec.TimeoutMessage != "" && IsTimeout(err) {
		return huma.Error504GatewayTimeout(spec.TimeoutMessage)
	}

	return huma.NewError(http.StatusBadGateway, spec.ClientMessage)
}

// ErrorLogFields builds the log fields of a failed call, adding the status
// code and body the service answered with, if any.
func ErrorLogFields(err error, extra ...any) []any {
	fields := []any{"error", err}
	fields = append(fields, extra...)

	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		fields = append(fields, "statusCode", statusErr.StatusCode, "responseBody", statusErr.Body)
	}

	return fields
}
