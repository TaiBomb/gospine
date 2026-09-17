// Package httpserver runs a gin engine with a Huma API mounted under
// {contextPath}/api, the /health and /status probes, and the request logging,
// request id and panic recovery middlewares every service shares.
//
// The service contributes its operations as Modules. Engine and API stay
// reachable for anything the options do not cover.
package httpserver

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strconv"

	"github.com/TaiBomb/gospine/apidoc"
	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/logging"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

// Server is the HTTP server of a service.
type Server struct {
	cfg        config.Server
	engine     *gin.Engine
	api        huma.API
	httpServer *http.Server
}

// New builds the server and registers every route; it does not listen yet.
func New(opts Options) *Server {
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()

	engine.Use(
		RequestLogger(
			WithQuietPaths(
				path.Join("/", opts.Config.ContextPath, "health"),
				path.Join("/", opts.Config.ContextPath, "status"),
			),
		),
		Recovery(),
	)
	engine.Use(opts.Middlewares...)

	s := &Server{
		cfg:    opts.Config,
		engine: engine,
	}

	s.registerRoutes(opts)

	log := logging.FromContext(context.Background())
	for _, r := range s.engine.Routes() {
		log.Info("Route registered", "method", r.Method, "path", r.Path)
	}

	return s
}

func (s *Server) registerRoutes(opts Options) {
	base := s.engine.Group(s.cfg.ContextPath)

	health := opts.HealthHandler
	if health == nil {
		health = NewHealthHandler()
	}

	status := opts.StatusHandler
	if status == nil {
		status = NewStatusHandler(s.cfg.PingTimeout, opts.Status)
	}

	base.GET("/health", health)
	base.GET("/status", status)

	apiGroup := base.Group("/api", opts.APIMiddlewares...)
	s.api = humagin.NewWithGroup(
		s.engine,
		apiGroup,
		apidoc.NewConfig(path.Join("/", s.cfg.ContextPath, "api"), opts.APIDoc),
	)

	for _, module := range opts.Modules {
		group := huma.NewGroup(s.api, module.Prefix)
		if module.Tag != "" {
			group.UseSimpleModifier(func(op *huma.Operation) {
				op.Tags = append(op.Tags, module.Tag)
			})
		}

		module.Register(group)
	}
}

// Engine returns the gin engine, to mount routes outside the Huma API.
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

// API returns the Huma API mounted under {contextPath}/api.
func (s *Server) API() huma.API {
	return s.api
}

// Start listens in the background; a listener failure exits the process.
func (s *Server) Start() error {
	port := ":" + strconv.Itoa(s.cfg.Port)

	s.httpServer = &http.Server{
		Addr:         port,
		Handler:      s.engine,
		ReadTimeout:  s.cfg.ReadTimeout,
		WriteTimeout: s.cfg.WriteTimeout,
	}

	log := logging.FromContext(context.Background())
	log.Info("HTTP server starting", "port", port)

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("HTTP server failed", "error", err)
		}
	}()

	return nil
}

// Shutdown stops the server gracefully; it is a no-op if Start was never called.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}

	logging.FromContext(ctx).Info("Shutting down HTTP server")

	return s.httpServer.Shutdown(ctx)
}
