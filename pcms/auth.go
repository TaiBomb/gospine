package pcms

import (
	"context"
	"net/http"

	"github.com/TaiBomb/gopcms"
)

// InternalAPIKeyHeader is the header PayloadCMS reads the internal API key from.
const InternalAPIKeyHeader = "x-internal-api-key"

type internalAPIKeyCtxKey struct{}

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

type internalAPIKeyAuth struct{}

var _ gopcms.Authenticator = internalAPIKeyAuth{}

// ApplyAuth leaves a request without a key unauthenticated rather than failing
// it: PayloadCMS decides what is readable anonymously.
func (internalAPIKeyAuth) ApplyAuth(ctx context.Context, req *http.Request) error {
	key, ok := InternalAPIKeyFromContext(ctx)
	if !ok {
		return nil
	}

	req.Header.Set(InternalAPIKeyHeader, key)

	return nil
}
