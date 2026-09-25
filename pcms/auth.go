package pcms

import (
	"context"
	"net/http"

	"github.com/TaiBomb/gopcms"
)

// InternalAPIKeyHeader is the header PayloadCMS reads the internal API key from.
const InternalAPIKeyHeader = "x-internal-api-key"

// AuthorizationHeader is the header PayloadCMS authenticates a user from.
const AuthorizationHeader = "Authorization"

type (
	internalAPIKeyCtxKey struct{}
	authorizationCtxKey  struct{}
)

// ContextWithInternalAPIKey returns a context carrying the internal API key.
func ContextWithInternalAPIKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, internalAPIKeyCtxKey{}, key)
}

// InternalAPIKeyFromContext returns the internal API key and whether a
// non-empty one was found.
func InternalAPIKeyFromContext(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(internalAPIKeyCtxKey{}).(string)
	return key, ok && key != ""
}

// ContextWithAuthorization returns a context carrying the value of the
// caller's Authorization header (e.g. "Bearer <token>"), which is then
// forwarded to PayloadCMS to act as that user.
func ContextWithAuthorization(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, authorizationCtxKey{}, value)
}

// AuthorizationFromContext returns the Authorization header value and
// whether a non-empty one was found.
func AuthorizationFromContext(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(authorizationCtxKey{}).(string)
	return value, ok && value != ""
}

// contextAuth sets on every call the credentials found in the context.
type contextAuth struct{}

var _ gopcms.Authenticator = contextAuth{}

// ApplyAuth leaves a request without credentials unauthenticated rather than
// failing it: PayloadCMS decides what is readable anonymously.
func (contextAuth) ApplyAuth(ctx context.Context, req *http.Request) error {
	if key, ok := InternalAPIKeyFromContext(ctx); ok {
		req.Header.Set(InternalAPIKeyHeader, key)
	}

	if value, ok := AuthorizationFromContext(ctx); ok {
		req.Header.Set(AuthorizationHeader, value)
	}

	return nil
}
