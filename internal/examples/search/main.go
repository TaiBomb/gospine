// Command search is a service backed by a store other than PayloadCMS, that
// extends the server with its own middleware, probe and routes. Package pcms
// is not imported, so neither it nor gopcms is compiled in.
//
//	env port=8080 go run ./internal/examples/search
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Shin-9x/envconfig"
	"github.com/TaiBomb/gospine/apidoc"
	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/httpclient"
	spine "github.com/TaiBomb/gospine/httpserver"
	"github.com/TaiBomb/gospine/logging"
	"github.com/TaiBomb/gospine/telemetry"
	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

// EnvConfig is the configuration of the service: no PayloadCMS keys.
type EnvConfig struct {
	config.Base
}

// searchClient stands for a client whose ping does not match spine.Probe,
// such as an Elasticsearch client.
type searchClient struct {
	http *http.Client
}

func (searchClient) Ping() (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK}, nil
}

// tenantMiddleware is a gin middleware living in the service.
func tenantMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if tenant := c.GetHeader("X-Tenant"); tenant != "" {
			c.Header("X-Tenant", tenant)
		}
		c.Next()
	}
}

// queries is a service-specific metric alongside those recorded by gospine
var queries = telemetry.NewCounter("search.queries", telemetry.WithDescription("Search queries served."))

type searchOutput struct {
	Body struct {
		Hits int `json:"hits"`
	}
}

func registerSearch(api huma.API, _ searchClient) {
	// A Huma middleware added here applies to this module only.
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		ctx.SetHeader("Cache-Control", "no-store")
		next(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "search",
		Method:      http.MethodGet,
		Path:        "/query",
		Summary:     "Search",
	}, func(ctx context.Context, _ *struct{}) (*searchOutput, error) {
		queries.Inc(ctx)
		return &searchOutput{}, nil
	})
}

func main() {
	logging.InitLogger()

	cfg, err := envconfig.Load[EnvConfig]()
	if err != nil {
		logging.Log.Fatal("Error loading config: ", "err", err)
	}

	logging.SetLevel(cfg.LogLevel)

	metrics, err := telemetry.Setup(context.Background(), cfg.Telemetry, telemetry.Service{Name: "search"})
	if err != nil {
		logging.Log.Fatal("Cannot set up telemetry", "err", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A custom Transport (TLS, proxy) would go in Options.Transport.
	search := searchClient{http: httpclient.New(httpclient.Options{Timeout: cfg.Client.Timeout}).HTTP()}

	server := spine.New(spine.Options{
		Config: cfg.Server,
		APIDoc: apidoc.Info{Title: "Search API", Version: "1.0.0"},
		Modules: []spine.Module{
			{Prefix: "/search", Tag: "Search", Register: func(api huma.API) { registerSearch(api, search) }},
		},
		Status: spine.Status{
			Probe: spine.ProbeFunc(func(ctx context.Context) error {
				_, err := search.Ping()
				return err
			}),
			Dependency: "elasticsearch",
		},
		HealthHandler: func(c *gin.Context) {
			c.JSON(http.StatusOK, spine.HealthResponse{Status: "ok", Timestamp: time.Now().UTC()})
		},
		Middlewares: []gin.HandlerFunc{tenantMiddleware()},
	})

	// Routes outside the Huma API go straight on the engine.
	server.Engine().GET("/version", func(c *gin.Context) { c.String(http.StatusOK, "1.0.0") })

	if err := server.Start(); err != nil {
		logging.Log.Fatal("Cannot start server", "err", err)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logging.Log.Error("Server forced to shutdown", "error", err)
	}
	if err := metrics(shutdownCtx); err != nil {
		logging.Log.Error("Telemetry shutdown failed", "error", err)
	}
}
