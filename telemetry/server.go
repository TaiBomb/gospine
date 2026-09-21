package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/TaiBomb/gospine/logging"
)

// startServer serves the scrape endpoint on its own port: out of the ingress,
// and out of the timeouts and traffic of the main server. It listens before
// returning, so a busy port fails Setup instead of the process later.
func startServer(port int, path string, scrape http.Handler) (*http.Server, error) {
	server := &http.Server{
		Addr: ":" + strconv.Itoa(port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			scrape.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, fmt.Errorf("telemetry: listen on %s: %w", server.Addr, err)
	}

	log := logging.FromContext(context.Background())
	log.Info("Metrics server starting", "port", server.Addr, "path", path)

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("Metrics server failed", "error", err)
		}
	}()

	return server, nil
}

// stopServer is a no-op when no server was started.
func stopServer(ctx context.Context, server *http.Server) error {
	if server == nil {
		return nil
	}

	return server.Shutdown(ctx)
}
