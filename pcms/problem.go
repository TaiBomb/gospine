package pcms

import (
	"context"
	"errors"
	"net/http"

	"github.com/TaiBomb/gopcms"
	"github.com/TaiBomb/gospine/logging"
	"github.com/danielgtaylor/huma/v2"
)

// ErrNotFound marks a lookup PayloadCMS answered without a matching document.
// Wrap it so Upstream answers 404 for queries that return an empty list.
var ErrNotFound = errors.New("no matching document in payloadcms")

// UpstreamSpec defines how a failed PayloadCMS call is reported.
type UpstreamSpec struct {
	LogMessage    string
	ClientMessage string

	// NotFoundMessage, when set, turns a not-found upstream into a 404.
	NotFoundMessage string
}

// Upstream logs a failed PayloadCMS call and maps it to an HTTP error: 404 for
// a not-found when configured, 502 otherwise, never exposing upstream details.
func Upstream(ctx context.Context, err error, spec UpstreamSpec, logFields ...any) error {
	log := logging.FromContext(ctx)

	if spec.NotFoundMessage != "" && isNotFound(err) {
		log.Debug(spec.LogMessage+": not found upstream", logFields...)
		return huma.Error404NotFound(spec.NotFoundMessage)
	}

	log.Error(spec.LogMessage, ErrorLogFields(err, logFields...)...)

	return huma.NewError(http.StatusBadGateway, spec.ClientMessage)
}

func isNotFound(err error) bool {
	return gopcms.IsNotFound(err) || errors.Is(err, ErrNotFound)
}

// ErrorLogFields builds the log fields of a failed PayloadCMS call, adding the
// upstream status code and body when err is an API error.
func ErrorLogFields(err error, extra ...any) []any {
	fields := []any{"error", err}
	fields = append(fields, extra...)

	if apiErr, ok := gopcms.AsAPIError(err); ok {
		fields = append(
			fields,
			"statusCode", apiErr.StatusCode,
			"responseBody", string(apiErr.RawBody),
		)
	}

	return fields
}
