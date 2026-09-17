# gospine

[![Go Reference](https://pkg.go.dev/badge/github.com/TaiBomb/gospine.svg)](https://pkg.go.dev/github.com/TaiBomb/gospine)
[![License: MIT](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

The backbone shared by Go HTTP services that serve a [Huma](https://huma.rocks)
API on [gin](https://gin-gonic.com), optionally reading their data from
[PayloadCMS](https://payloadcms.com) through
[gopcms](https://github.com/TaiBomb/gopcms).

It holds what every such service would otherwise copy: logging with request
correlation, configuration, the outbound HTTP client, the HTTP server with its
middlewares and probes, the OpenAPI setup, pagination and PayloadCMS helpers.
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

Dependencies between packages only go one way: `logging`, `config`, `paging`
and `apidoc` import nothing from the module; `httpclient` uses `logging`;
`httpserver` uses `logging`, `config`, `apidoc`; `pcms` uses `logging`,
`paging` and `pcms` is the only package depending on gopcms (see
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

### Access log

One `Request handled` line per request: error on 5xx, warn on 4xx, debug
otherwise (always debug for `/health` and `/status`). Fields: `status`,
`method`, `path`, `latency`, `latency_ms`, `resp_size_bytes`, and when present
`query`, `path_params`, `user_agent`, `req_size_bytes`, `req_body` (textual
bodies of POST/PUT/PATCH/DELETE, 4 KiB cap), `req_body_truncated`,
`gin_errors`, `resp_body` (only for status >= 400, 4 KiB cap).

Every request gets its own id, returned in `X-Request-ID`; an incoming
`X-Request-ID` is kept as `correlation_id` when it is printable ASCII of at
most 128 characters.

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
| A gin middleware on every route                                        | `Options.Middlewares` (runs after the access log and recovery)                                                                    |
| A gin middleware on `/api` only                                        | `Options.APIMiddlewares`                                                                                                          |
| A middleware on one module only                                        | `api.UseMiddleware(...)` inside `Module.Register` (Huma middleware)                                                               |
| Routes outside the Huma API                                            | `Server.Engine()`                                                                                                                 |
| Huma settings, transformers, extra operations                          | `Server.API()`                                                                                                                    |
| Own `/health` or `/status`                                             | `Options.HealthHandler`, `Options.StatusHandler`; wrap `NewHealthHandler()` / `NewStatusHandler(...)` to extend the built-in ones |
| Any dependency in `/status`                                            | `Status.Probe`, `ProbeFunc`                                                                                                       |
| TLS, proxy, custom dialer or instrumented transport for outbound calls | `httpclient.Options.Transport` (any `http.RoundTripper`; `X-Request-ID` is still forwarded)                                       |
| Any PayloadCMS collection, or a type composed around the connection    | `pcms.Client.Raw()`                                                                                                               |
| Middlewares, OpenAPI config or HTTP client without `Server`            | `RequestLogger`, `Recovery`, `HeaderForwarder`, `apidoc.NewConfig`, `httpclient.New` work on their own                            |
| Different settings or defaults                                         | `config.Base` is a plain struct: declare your own fields instead of embedding it                                                  |

The built-in routes keep their access log behavior when replaced: `/health`
and `/status` are always logged at debug level.
