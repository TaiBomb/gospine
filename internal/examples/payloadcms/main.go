// Command payloadcms is a complete service reading from PayloadCMS: the
// main.go of a service built on gospine.
//
// Run it with at least port and payloadCms.baseUrl in the environment:
//
//	env port=8080 payloadCms.baseUrl=http://localhost:3000 go run ./internal/examples/payloadcms
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Shin-9x/envconfig"
	"github.com/TaiBomb/gospine/apidoc"
	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/httpclient"
	spine "github.com/TaiBomb/gospine/httpserver"
	"github.com/TaiBomb/gospine/logging"
	"github.com/TaiBomb/gospine/pcms"
	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

// EnvConfig is the configuration of the service.
type EnvConfig struct {
	config.Base

	PayloadCMS pcms.Config
}

func (c EnvConfig) String() string { return envconfig.Mask(c) }

type greetingOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

// registerGreetings stands for a service's own module.
func registerGreetings(api huma.API, _ pcms.Client) {
	huma.Register(api, huma.Operation{
		OperationID: "greet",
		Method:      http.MethodGet,
		Path:        "/hello",
		Summary:     "Say hello",
	}, func(ctx context.Context, _ *struct{}) (*greetingOutput, error) {
		out := &greetingOutput{}
		out.Body.Message = "hello"
		return out, nil
	})
}

func main() {
	logging.InitLogger()
	logging.Log.Info("STARTING Service example")

	cfg, err := envconfig.Load[EnvConfig]()
	if err != nil {
		logging.Log.Fatal("Error loading config: ", "err", err)
	}
	logging.Log.Info("ENV-conf loaded", "config", cfg.String())

	if cfg.LogLevel != "" {
		logging.SetLevel(cfg.LogLevel)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpClient := httpclient.New(httpclient.Options{Timeout: cfg.Client.Timeout})

	cmsClient, err := pcms.New(cfg.PayloadCMS, httpClient.HTTP())
	if err != nil {
		logging.Log.Fatal("Error creating PayloadCMS client: ", "err", err)
	}

	server := spine.New(spine.Options{
		Config: cfg.Server,
		APIDoc: apidoc.Info{
			Title:       "Example API",
			Version:     "1.0.0",
			Description: "Says hello.",
		},
		Modules: []spine.Module{
			{Prefix: "/greetings", Tag: "Greetings", Register: func(api huma.API) { registerGreetings(api, cmsClient) }},
		},
		Status: spine.Status{
			Probe:       cmsClient,
			Dependency:  "payloadcms",
			ErrorFields: pcms.ErrorLogFields,
		},
		APIMiddlewares: []gin.HandlerFunc{
			spine.HeaderForwarder(pcms.InternalAPIKeyHeader, pcms.ContextWithInternalAPIKey),
		},
	})
	if err := server.Start(); err != nil {
		logging.Log.Fatal("Cannot start server", "err", err)
	}

	<-ctx.Done()
	logging.Log.Info("Shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logging.Log.Error("Server forced to shutdown", "error", err)
	}

	logging.Log.Info("STOPPED Service example")
}
