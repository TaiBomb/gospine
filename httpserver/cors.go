package httpserver

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// PermissiveCORS allows every origin and method, with credentials, caching
// preflights for 12h. Headers are sent in the order given.
func PermissiveCORS(allowHeaders, exposeHeaders []string) gin.HandlerFunc {
	return cors.New(cors.Config{
		AllowAllOrigins: true,
		AllowMethods: []string{
			"GET",
			"POST",
			"PUT",
			"PATCH",
			"DELETE",
			"OPTIONS",
		},
		AllowHeaders:     allowHeaders,
		ExposeHeaders:    exposeHeaders,
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}
