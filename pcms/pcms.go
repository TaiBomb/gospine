package pcms

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/TaiBomb/gopcms"
	"github.com/TaiBomb/gospine/logging"
)

type client struct {
	pcms *gopcms.Client
}

// New returns a Client that issues every call through httpClient, sharing its
// pool and timeout, authenticated with the internal API key in the context.
// Ping is the exception: gopcms issues it unauthenticated.
func New(cfg Config, httpClient *http.Client) (Client, error) {
	pcms, err := gopcms.New(
		cfg.BaseURL,
		gopcms.WithHTTPClient(httpClient),
		gopcms.WithAPIPrefix(cfg.APIURL),
		gopcms.WithAuth(internalAPIKeyAuth{}),
		gopcms.WithObserver(newObserver(cfg)),
	)
	if err != nil {
		return nil, fmt.Errorf("create gopcms client: %w", err)
	}

	return &client{
		pcms: pcms,
	}, nil
}

// newObserver logs every attempt, retries included, at debug level and
// records it in the PayloadCMS client metrics.
func newObserver(cfg Config) gopcms.Observer {
	metrics := newClientMetrics(cfg)

	return gopcms.ObserverFunc{
		Response: func(ctx context.Context, method, url string, statusCode int, duration time.Duration, err error) {
			logging.FromContext(ctx).Debug(
				"payloadcms call",
				"method", method,
				"url", url,
				"status", statusCode,
				"duration", duration.String(),
				"duration_ms", logging.Millis(duration),
				"err", err,
			)

			metrics.record(ctx, method, url, statusCode, duration, err)
		},
	}
}

// Ping issues a GET against PayloadCMS's /access endpoint.
func (c *client) Ping(ctx context.Context) error {
	return c.pcms.Ping(ctx)
}

func (c *client) Raw() *gopcms.Client {
	return c.pcms
}
