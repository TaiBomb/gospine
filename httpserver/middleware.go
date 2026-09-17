package httpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/TaiBomb/gospine/logging"
	"github.com/gin-gonic/gin"
)

const (
	maxLoggedBodyBytes     = 4 << 10 // 4 KiB
	maxCorrelationIDLength = 128
)

// HeaderForwarder puts an incoming header into the request context, so calls
// made while serving the request can pick it up. Absent or empty headers are skipped.
func HeaderForwarder(header string, into func(context.Context, string) context.Context) gin.HandlerFunc {
	return func(c *gin.Context) {
		if value := c.GetHeader(header); value != "" {
			c.Request = c.Request.WithContext(into(c.Request.Context(), value))
		}

		c.Next()
	}
}

type requestLoggerConfig struct {
	quietPaths map[string]struct{}
}

// RequestLoggerOption customizes RequestLogger.
type RequestLoggerOption func(*requestLoggerConfig)

// WithQuietPaths keeps the given routes at debug level whatever they answer.
// Paths are matched against the registered route, context path included.
func WithQuietPaths(paths ...string) RequestLoggerOption {
	return func(cfg *requestLoggerConfig) {
		if cfg.quietPaths == nil {
			cfg.quietPaths = make(map[string]struct{}, len(paths))
		}

		for _, p := range paths {
			cfg.quietPaths[p] = struct{}{}
		}
	}
}

// RequestLogger gives each request its own id and writes one access log line
// for it: error on 5xx, warn on 4xx, debug otherwise.
// Register it before Recovery, or panics are answered without being logged.
func RequestLogger(opts ...RequestLoggerOption) gin.HandlerFunc {
	var cfg requestLoggerConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(c *gin.Context) {
		start := time.Now()

		attachRequestID(c)

		body, bodyTruncated := captureRequestBody(c)

		recorder := &responseRecorder{ResponseWriter: c.Writer}
		c.Writer = recorder

		defer func() {
			logRequest(c, cfg, start, body, bodyTruncated, recorder)
		}()

		c.Next()
	}
}

// Recovery turns a panic into a 500 and logs it with its stack through the
// service logger, instead of gin's own format on stderr.
func Recovery() gin.HandlerFunc {
	// A nil writer switches gin's own reporting off.
	return gin.CustomRecoveryWithWriter(
		nil,
		func(c *gin.Context, recovered any) {
			logging.FromContext(c.Request.Context()).Error(
				"Panic recovered while serving the request",
				"panic", fmt.Sprint(recovered),
				"method", c.Request.Method,
				"path", c.Request.URL.Path,
				"stack", string(debug.Stack()),
			)

			c.AbortWithStatus(http.StatusInternalServerError)
		},
	)
}

func logRequest(
	c *gin.Context,
	cfg requestLoggerConfig,
	start time.Time,
	body string,
	bodyTruncated bool,
	recorder *responseRecorder,
) {
	latency := time.Since(start)
	status := c.Writer.Status()

	// gin reports -1 when nothing was written.
	responseSize := max(c.Writer.Size(), 0)

	fields := []any{
		"status", status,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"latency", latency.String(),
		"latency_ms", logging.Millis(latency),
		"resp_size_bytes", responseSize,
	}

	if query := c.Request.URL.RawQuery; query != "" {
		fields = append(fields, "query", query)
	}

	if params := pathParams(c); len(params) > 0 {
		fields = append(fields, "path_params", params)
	}

	if userAgent := c.Request.UserAgent(); userAgent != "" {
		fields = append(fields, "user_agent", userAgent)
	}

	if size := c.Request.ContentLength; size > 0 {
		fields = append(fields, "req_size_bytes", size)
	}

	if body != "" {
		fields = append(fields, "req_body", body)

		if bodyTruncated {
			fields = append(fields, "req_body_truncated", true)
		}
	}

	if ginErrors := strings.TrimSpace(c.Errors.String()); ginErrors != "" {
		fields = append(fields, "gin_errors", ginErrors)
	}

	if responseBody := recorder.capturedBody(); responseBody != "" {
		fields = append(fields, "resp_body", responseBody)
	}

	logging.FromContext(c.Request.Context()).Log(
		c.Request.Context(),
		levelFor(status, cfg.isQuiet(c.FullPath())),
		"Request handled",
		fields...,
	)
}

func (cfg requestLoggerConfig) isQuiet(route string) bool {
	_, quiet := cfg.quietPaths[route]
	return quiet
}

func levelFor(status int, quiet bool) slog.Level {
	switch {
	case quiet:
		return slog.LevelDebug
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}

func pathParams(c *gin.Context) map[string]string {
	if len(c.Params) == 0 {
		return nil
	}

	params := make(map[string]string, len(c.Params))
	for _, param := range c.Params {
		params[param.Key] = param.Value
	}

	return params
}

// attachRequestID always generates our own id, so it identifies exactly one
// request; the caller's id is kept only as the correlation id.
func attachRequestID(c *gin.Context) string {
	id := rand.Text()

	ctx := logging.ContextWithRequestID(c.Request.Context(), id)

	if correlationID := sanitizeCorrelationID(c.GetHeader(logging.RequestIDHeader)); correlationID != "" {
		ctx = logging.ContextWithCorrelationID(ctx, correlationID)
	}

	c.Writer.Header().Set(logging.RequestIDHeader, id)
	c.Request = c.Request.WithContext(ctx)

	return id
}

// sanitizeCorrelationID returns id if it is printable ASCII within the length
// cap, the empty string otherwise.
func sanitizeCorrelationID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > maxCorrelationIDLength {
		return ""
	}

	for _, r := range id {
		if r < '!' || r > '~' {
			return ""
		}
	}

	return id
}

// captureRequestBody reads the body up to the logging cap and puts it back
// for the handler, reporting whether it went past the cap.
func captureRequestBody(c *gin.Context) (string, bool) {
	req := c.Request
	if req == nil || req.Body == nil || req.Body == http.NoBody || !hasLoggableBody(req) {
		return "", false
	}

	// One byte past the cap tells a body that just fits from a truncated one.
	head, err := io.ReadAll(io.LimitReader(req.Body, maxLoggedBodyBytes+1))
	if err != nil {
		return "", false
	}

	rest := req.Body
	req.Body = readCloser{
		Reader: io.MultiReader(bytes.NewReader(head), rest),
		Closer: rest,
	}

	truncated := len(head) > maxLoggedBodyBytes
	if truncated {
		head = head[:maxLoggedBodyBytes]
	}

	return formatBody(head), truncated
}

// hasLoggableBody reports whether the method carries a body and its format
// reads as text; uploads and binary payloads are left out on purpose.
func hasLoggableBody(req *http.Request) bool {
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}

	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		return false
	}

	switch {
	case mediaType == "application/json", strings.HasSuffix(mediaType, "+json"):
		return true
	case mediaType == "application/x-www-form-urlencoded":
		return true
	case strings.HasPrefix(mediaType, "text/"):
		return true
	default:
		return false
	}
}

// formatBody compacts JSON onto one line and leaves anything else as it is.
func formatBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var compacted bytes.Buffer
	if err := json.Compact(&compacted, body); err != nil {
		return string(body)
	}

	return compacted.String()
}

type readCloser struct {
	io.Reader
	io.Closer
}
