# gospine

[![Go Reference](https://pkg.go.dev/badge/github.com/TaiBomb/gospine.svg)](https://pkg.go.dev/github.com/TaiBomb/gospine)
[![License: MIT](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

The backbone shared by Go HTTP services that serve a [Huma](https://huma.rocks)
API on [gin](https://gin-gonic.com), optionally reading their data from
[PayloadCMS](https://payloadcms.com) through
[gopcms](https://github.com/TaiBomb/gopcms).

It holds what every such service would otherwise copy: logging with request
correlation, configuration, the outbound HTTP client, the HTTP server with its
middlewares and probes, the OpenAPI setup, pagination, PayloadCMS helpers and
OpenTelemetry metrics.
The domain — operations, DTOs, collections — stays in the service.

## Install

```sh
go get github.com/TaiBomb/gospine
```

## Packages

| Package         | What it gives you                                                                                                 |
|-----------------|-------------------------------------------------------------------------------------------------------------------|
| `logging`       | JSON `slog` logger, `FromContext` adding `request_id` / `correlation_id`, `Millis`                                |
| `config`        | `Base`, `Server`, `Client`: settings to embed in the service's `EnvConfig`                                        |
| `httpclient`    | Shared `*http.Client` forwarding `X-Request-ID` on outbound calls                                                 |
| `httpserver`    | `Server` with `Module`s, `/health`, `/status`, access log, recovery, `HeaderForwarder`, `PermissiveCORS`          |
| `apidoc`        | Huma config: servers, no `$schema` injection, schema names prefixed by area                                       |
| `paging`        | `Page[T]`, `PagedResponse[T]`, `NormalizePagination`, `NewInMemoryPage`                                           |
| `pcms`          | PayloadCMS `Client`, internal API key auth, `Upstream` error mapping, `NewPage`, `BuildSort`, `UnmarshalRelation` |
| `pcms/pcmstest` | `MockClient` for handler tests                                                                                    |
| `telemetry`     | OpenTelemetry metrics: `Setup` (Prometheus or OTLP), `Meter` for the service's own metrics, `Enabled`, `Handler`  |

Dependencies between packages only go one way: `logging`, `config`, `paging`
and `apidoc` import nothing from the module; `telemetry` uses `logging`,
`config`; `httpclient` uses `logging`, `telemetry`; `httpserver` uses
`logging`, `config`, `apidoc`, `telemetry`; `pcms` uses `logging`, `paging`,
`telemetry` and `pcms` is the only package depending on gopcms (see
[Without PayloadCMS](#without-payloadcms)).

## Usage

Two runnable services live in [`internal/examples/`](internal/examples): a
complete one reading from PayloadCMS in
[`internal/examples/payloadcms`](internal/examples/payloadcms/main.go), and one
without PayloadCMS that extends the server in
[`internal/examples/search`](internal/examples/search/main.go). Being under
`internal/`, they are invisible to services; CI builds them, so they cannot
drift from the code. In short:

```go
type EnvConfig struct {
	config.Base // logLevel, shutdownTimeout, clientTimeout, port, contextPath, ...

	PayloadCMS pcms.Config // payloadCms.baseUrl, payloadCms.apiUrl
}

// First: the client and the server check whether telemetry is on.
shutdownTelemetry, err := telemetry.Setup(context.Background(), cfg.Telemetry, telemetry.Service{Name: "example"})

httpClient := httpclient.New(httpclient.Options{Timeout: cfg.Client.Timeout})

cmsClient, err := pcms.New(cfg.PayloadCMS, httpClient.HTTP())

server := spine.New(spine.Options{
	Config: cfg.Server,
	APIDoc: apidoc.Info{Title: "Example API", Version: "1.0.0", Description: "..."},
	Modules: []spine.Module{
		{Prefix: "/greetings", Tag: "Greetings", Register: func(api huma.API) { greetings.Register(api, cmsClient) }},
	},
	Status: spine.Status{Probe: cmsClient, Dependency: "payloadcms", ErrorFields: pcms.ErrorLogFields},
	APIMiddlewares: []gin.HandlerFunc{
		spine.HeaderForwarder(pcms.InternalAPIKeyHeader, pcms.ContextWithInternalAPIKey),
	},
})
```

Services usually have a local `httpserver` package too: import this one as
`spine`.

### Routes

| Route                                                                           | Answer                                                                                                                                           |
|---------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------|
| `GET {contextPath}/health`                                                      | `200 {"status":"ok","timestamp":...}`                                                                                                            |
| `GET {contextPath}/status`                                                      | pings `Status.Probe` within `pingTimeout`: `200` or `503 {"status":"error","error":"<dependency> unreachable"}`; without a probe, like `/health` |
| `{contextPath}/api/{module prefix}/...`                                         | the module's Huma operations                                                                                                                     |
| `{contextPath}/api/openapi.json`, `/openapi.yaml`, `/openapi-3.0.json`, `/docs` | the generated document                                                                                                                           |
| `GET {contextPath}{telemetry.path}`                                             | the Prometheus scrape, only with `telemetry.enabled=true` and `telemetry.port=0` (see [Metrics](#metrics))                                       |

### Configuration keys

Keys are flat and unchanged by embedding:

| Key                  | Default                            |
|----------------------|------------------------------------|
| `logLevel`           | `INFO`                             |
| `shutdownTimeout`    | `10s`                              |
| `clientTimeout`      | `10s`                              |
| `port`               | required                           |
| `contextPath`        | `/`                                |
| `pingTimeout`        | `2s`                               |
| `readTimeout`        | `5s`                               |
| `writeTimeout`       | `5s`                               |
| `payloadCms.baseUrl` | required (only with `pcms.Config`) |
| `payloadCms.apiUrl`  | `/api` (only with `pcms.Config`)   |

The telemetry keys come with `config.Base` as well:

| Key                       | Default                                               |
|---------------------------|-------------------------------------------------------|
| `telemetry.enabled`       | `false`                                               |
| `telemetry.exporter`      | `prometheus`; or `otlp`                               |
| `telemetry.serviceName`   | the name passed to `telemetry.Setup`                  |
| `telemetry.port`          | `9464`; `0` serves the metrics on the main server     |
| `telemetry.path`          | `/metrics`                                            |
| `telemetry.interval`      | `30s` (otlp push interval)                            |
| `telemetry.otlp.endpoint` | empty: the `OTEL_EXPORTER_OTLP_*` variables apply     |
| `telemetry.otlp.insecure` | `false`                                               |

### Access log

One `Request handled` line per request: error on 5xx, warn on 4xx, debug
otherwise (always debug for `/health`, `/status` and the scrape endpoint). Fields: `status`,
`method`, `path`, `latency`, `latency_ms`, `resp_size_bytes`, and when present
`query`, `path_params`, `user_agent`, `req_size_bytes`, `req_body` (textual
bodies of POST/PUT/PATCH/DELETE, 4 KiB cap), `req_body_truncated`,
`gin_errors`, `resp_body` (only for status >= 400, 4 KiB cap).

Every request gets its own id, returned in `X-Request-ID`; an incoming
`X-Request-ID` is kept as `correlation_id` when it is printable ASCII of at
most 128 characters.

## Metrics

Off by default: with `telemetry.enabled=false` no provider is installed, no
port is opened and no middleware is registered. The service calls
`telemetry.Setup` once, before building the client and the server, and shuts
it down after the server:

```go
shutdownTelemetry, err := telemetry.Setup(context.Background(), cfg.Telemetry, telemetry.Service{Name: "rt-rsa-api"})
if err != nil {
	logging.Log.Fatal("Cannot set up telemetry", "err", err)
}

// httpclient.New, pcms.New, spine.New, Start, <-ctx.Done(), server.Shutdown(shutdownCtx)

if err := shutdownTelemetry(shutdownCtx); err != nil {
	logging.Log.Error("Telemetry shutdown failed", "error", err)
}
```

The rest is configuration:

| Mode                                 | Keys                                                 | Metrics                                                                                                                                                                                                  |
|--------------------------------------|------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Prometheus, dedicated port (default) | `telemetry.port=9464`                                | served on `:9464/metrics`: out of the ingress, of the API traffic and of `readTimeout` / `writeTimeout`                                                                                                  |
| Prometheus, main server              | `telemetry.port=0`                                   | served on `{contextPath}{telemetry.path}` of the API server, outside `/api` and the OpenAPI document, under its `readTimeout` / `writeTimeout`; reachable wherever the API port is, the ingress included |
| OTLP                                 | `telemetry.exporter=otlp`, `telemetry.otlp.endpoint` | pushed over OTLP/HTTP every `telemetry.interval`, and once more on shutdown; `/v1/metrics` is appended to an endpoint without a path                                                                     |

`service.name` is `telemetry.serviceName`, else the name passed to `Setup`;
`OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` win over both.
`service.version` is `Service.Version`, else the module version stamped in the
binary.

| OpenTelemetry                                                     | Prometheus                                                                           | Type      | Attributes                                                                                                     |
|-------------------------------------------------------------------|--------------------------------------------------------------------------------------|-----------|----------------------------------------------------------------------------------------------------------------|
| `http.server.request.duration`                                    | `http_server_request_duration_seconds`                                               | histogram | `http.request.method`, `http.route`, `http.response.status_code`, `url.scheme`, `error.type` (5xx only)        |
| `http.server.active_requests`                                     | `http_server_active_requests`                                                        | gauge     | `http.request.method`, `url.scheme`                                                                            |
| `http.server.request.body.size`, `http.server.response.body.size` | `http_server_request_body_size_bytes`, `http_server_response_body_size_bytes`        | histogram | as the duration                                                                                                |
| `http.client.request.duration`, `http.client.request.body.size`   | `http_client_request_duration_seconds`, `http_client_request_body_size_bytes`        | histogram | `http.request.method`, `server.address`, `server.port`, `http.response.status_code`, `error.type`, `network.*` |
| `payloadcms.client.request.duration`                              | `payloadcms_client_request_duration_seconds`                                         | histogram | `http.request.method`, `payloadcms.collection`, `http.response.status_code`, `error.type`                      |
| `payloadcms.client.requests`                                      | `payloadcms_client_requests_total`                                                   | counter   | as above                                                                                                       |
| Go runtime                                                        | `go_memory_used_bytes`, `go_goroutine_count`, `go_memory_gc_goal_bytes`, `go_*`, ... | various   |                                                                                                                |
| resource                                                          | `target_info`                                                                        | info      | `service.name`, `service.version`, `process.runtime.*`, `telemetry.sdk.*`                                      |

Every series also carries `otel_scope_name`, the package that records it.

- `http.route` is the route template (`/api/items/:id`), never the raw path;
  unrouted requests carry none, and methods outside the standard ones are
  `_OTHER`. No attribute holds a path, query, id or header.
- `/health`, `/status` and the scrape endpoint are not recorded.
- Server-sent event streams last as long as the client listens: they stay out
  of the duration and response size, and still count in `active_requests` and
  in the request size, whose `_count` is the number of streams opened.
- Durations use the semconv buckets, 5ms to 10s; the server adds 30s and 60s
  for exports and PDFs. Sizes go from 256 B to 64 MiB.
- PayloadCMS calls show up twice, on purpose: in `http.client.*` by host, as
  the transport sees them, and in `payloadcms.client.*` by collection, once per
  attempt, so each retry counts. `payloadcms.collection` is the first segment
  after the API prefix (`globals/<slug>` for globals, `root` for the prefix
  itself, `_OTHER` outside it); `error.type` is `http_<status>`, `timeout`,
  `canceled`, `network` or `invalid_response`.
- A collection name becomes a label only after PayloadCMS has answered 2xx for
  it, and at most 64 do: a service may take the collection from the caller, and
  an invented one, answered 404, stays `_OTHER`. So do the calls to a
  collection made before its first success.

## Without PayloadCMS

Only package `pcms` depends on PayloadCMS. A service backed by another store
(Elasticsearch, a database, another API) uses `config`, `logging`,
`httpclient`, `httpserver` and `paging`, and never imports `pcms`: neither it
nor gopcms is compiled in. Its dependency becomes the `/status` probe through
`Status.Probe`; `spine.ProbeFunc` adapts clients whose ping has another
signature:

```go
Status: spine.Status{
	Probe:      spine.ProbeFunc(func(ctx context.Context) error { _, err := es.Ping(); return err }),
	Dependency: "elasticsearch",
},
```

## Extension points

gospine must never be the reason a service cannot do something. What is
specific to a service lives in the service and plugs in here:

| Need                                                                   | How                                                                                                                               |
|------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| A gin middleware on every route                                        | `Options.Middlewares` (runs after the metrics, the access log and recovery)                                                       |
| A gin middleware on `/api` only                                        | `Options.APIMiddlewares`                                                                                                          |
| A middleware on one module only                                        | `api.UseMiddleware(...)` inside `Module.Register` (Huma middleware)                                                               |
| Routes outside the Huma API                                            | `Server.Engine()`                                                                                                                 |
| Huma settings, transformers, extra operations                          | `Server.API()`                                                                                                                    |
| Own `/health` or `/status`                                             | `Options.HealthHandler`, `Options.StatusHandler`; wrap `NewHealthHandler()` / `NewStatusHandler(...)` to extend the built-in ones |
| Any dependency in `/status`                                            | `Status.Probe`, `ProbeFunc`                                                                                                       |
| TLS, proxy, custom dialer or instrumented transport for outbound calls | `httpclient.Options.Transport` (any `http.RoundTripper`; `X-Request-ID` is still forwarded)                                       |
| Any PayloadCMS collection, or a type composed around the connection    | `pcms.Client.Raw()`                                                                                                               |
| Middlewares, OpenAPI config or HTTP client without `Server`            | `RequestLogger`, `Recovery`, `HeaderForwarder`, `Metrics`, `apidoc.NewConfig`, `httpclient.New` work on their own                 |
| Metrics of the service's own                                           | `telemetry.Meter(name)`: same provider and exporter, no-op while telemetry is off                                                 |
| No HTTP server metrics on one server                                   | `Options.DisableMetrics`                                                                                                          |
| The scrape endpoint on a server built without `Server`                 | mount `telemetry.Handler()`, which answers `ok` only with the Prometheus exporter and `telemetry.port=0`                          |
| Different settings or defaults                                         | `config.Base` is a plain struct: declare your own fields instead of embedding it                                                  |

The built-in routes keep their access log behavior when replaced: `/health`
and `/status` are always logged at debug level.
