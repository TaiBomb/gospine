package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/TaiBomb/gospine/logging"
	"github.com/gin-gonic/gin"
)

// HealthResponse is the body of /health and /status.
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error,omitempty"`
}

// NewHealthHandler returns the built-in /health handler: always 200 "ok".
func NewHealthHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		logging.FromContext(c.Request.Context()).Debug("Health check called", "status", "ok")

		c.JSON(http.StatusOK, HealthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC(),
		})
	}
}

// NewStatusHandler returns the built-in /status handler: 200 when status.Probe
// answers within pingTimeout (or is nil), 503 otherwise. A pingTimeout <= 0 sets no timeout.
func NewStatusHandler(pingTimeout time.Duration, status Status) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		if status.Probe != nil {
			if pingTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, pingTimeout)
				defer cancel()
			}

			if err := status.Probe.Ping(ctx); err != nil {
				reportUnreachable(ctx, c, status, err)
				return
			}
		}

		logging.FromContext(ctx).Debug("Status check called", "status", "ok")

		c.JSON(http.StatusOK, HealthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC(),
		})
	}
}

func reportUnreachable(ctx context.Context, c *gin.Context, status Status, err error) {
	dependency := status.Dependency
	if dependency == "" {
		dependency = "dependency"
	}

	fields := []any{"error", err}
	if status.ErrorFields != nil {
		fields = status.ErrorFields(err)
	}

	logging.FromContext(ctx).Error("Status check failed: "+dependency+" unreachable", fields...)

	c.JSON(http.StatusServiceUnavailable, HealthResponse{
		Status:    "error",
		Timestamp: time.Now().UTC(),
		Error:     dependency + " unreachable",
	})
}
