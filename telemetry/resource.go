package telemetry

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/logging"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func newResource(ctx context.Context, cfg config.Telemetry, svc Service) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cmp.Or(cfg.ServiceName, svc.Name, filepath.Base(os.Args[0]))),
	}
	if version := cmp.Or(svc.Version, buildVersion()); version != "" {
		attrs = append(attrs, semconv.ServiceVersion(version))
	}

	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithAttributes(attrs...),
		resource.WithFromEnv(),
	)
	if errors.Is(err, resource.ErrPartialResource) {
		logging.FromContext(ctx).Warn("Telemetry resource is incomplete", "error", err)
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	return res, nil
}

// buildVersion is the main module version stamped by the go command, if any.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "(devel)" {
		return ""
	}

	return info.Main.Version
}
