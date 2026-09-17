package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TaiBomb/gospine/logging"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// TestPermissiveCORS_MatchesTheLiteralConfig compares the middleware with the
// cors.Config it replaces, on a preflight and on a simple request.
func TestPermissiveCORS_MatchesTheLiteralConfig(t *testing.T) {
	allow := []string{"Origin", "Content-Type", "Accept", "Authorization", "x-api-key", logging.RequestIDHeader}
	expose := []string{"Content-Length", "Content-Disposition", logging.RequestIDHeader}

	literal := cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     allow,
		ExposeHeaders:    expose,
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})

	engineWith := func(mw gin.HandlerFunc) *gin.Engine {
		engine := gin.New()
		engine.Use(mw)
		engine.Any("/thing", func(c *gin.Context) { c.Status(http.StatusOK) })
		return engine
	}

	preflight := func() *http.Request {
		req := httptest.NewRequest(http.MethodOptions, "/thing", nil)
		req.Header.Set("Origin", "https://frontend.test")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		return req
	}

	simple := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/thing", nil)
		req.Header.Set("Origin", "https://frontend.test")
		return req
	}

	for name, build := range map[string]func() *http.Request{"preflight": preflight, "simple": simple} {
		t.Run(name, func(t *testing.T) {
			want := serve(engineWith(literal), build())
			got := serve(engineWith(PermissiveCORS(allow, expose)), build())

			if want.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Fatalf("expected the literal config to answer as CORS, got %v", want.Header())
			}

			if got.Code != want.Code {
				t.Errorf("expected status %d, got %d", want.Code, got.Code)
			}

			for header, values := range want.Header() {
				if got.Header().Get(header) != values[0] {
					t.Errorf("header %s: expected %q, got %q", header, values[0], got.Header().Get(header))
				}
			}
			if len(got.Header()) != len(want.Header()) {
				t.Errorf("expected headers %v, got %v", want.Header(), got.Header())
			}
		})
	}

	if got := serve(engineWith(PermissiveCORS(allow, expose)), preflight()).Header().Get("Access-Control-Max-Age"); got != "43200" {
		t.Errorf("expected a 12h max age, got %q", got)
	}
}
