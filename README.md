# nochaos-sdk

The contract between the **nochaos** platform and its **plugins**.

nochaos is an observability platform built as a backbone plus plugins:

| Layer | What it is | Where it lives |
|---|---|---|
| **Platform** | The backbone: sign-in, accounts, permissions, feature flags, configuration as code, the portal shell, the plugin host | `nochaos` |
| **Plugins** | One job each (alerts, logs, APM, internet status...), with their own routes, data, lifecycle and release | `nochaos-plugins` |
| **Tools** | The open-source software plugins are built on (VictoriaMetrics, ClickHouse, Grafana, OpenTelemetry Collector...). nochaos is a layer above them | catalog in `nochaos-deploy` |

This repository is what a plugin author needs, and nothing else. It is
Apache-2.0 so a plugin can carry any license.

## Write a plugin in 5 minutes

A plugin is a folder with a manifest and code that matches it.

`plugin.yaml`:

```yaml
apiVersion: nochaos.io/plugin/v1
id: hello
name: Hello
version: 0.1.0
summary: Says hello to the account.
requires:
  nochaos: ">=0.1 <1"
flag:
  default: false
api:
  - {method: GET, path: /api/hello, access: read}
jobs:
  - {name: tick, every: 1m}
```

`hello.go`:

```go
package hello

import (
	"context"
	_ "embed"
	"net/http"

	"github.com/nochaosio/nochaos-sdk/manifest"
	"github.com/nochaosio/nochaos-sdk/plugin"
)

//go:embed plugin.yaml
var manifestYAML []byte

type Plugin struct{ m manifest.Manifest }

func New() *Plugin {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic(err) // a broken manifest is a build-time bug
	}
	return &Plugin{m: m}
}

func (p *Plugin) Manifest() manifest.Manifest { return p.m }

func (p *Plugin) Start(_ context.Context, h plugin.Host) error {
	h.Handle("GET", "/api/hello", func(w http.ResponseWriter, _ *http.Request, s plugin.Session) {
		_, _ = w.Write([]byte("hello, " + s.AccountID))
	})
	h.Every("tick", func(context.Context) error { return nil })
	return nil
}

func (p *Plugin) Stop(context.Context) error { return nil }
```

`hello_test.go`:

```go
func TestConformance(t *testing.T) { plugintest.Conformance(t, hello.New(), nil) }
```

That one test proves the manifest is valid, the code registers exactly what
the manifest declares, and the plugin starts and stops.

## What the platform does for a plugin

A handler never checks who is calling. Before it runs, the platform has
already checked:

1. the caller is signed in (or holds a valid key);
2. the plugin's feature flag is on for the account (`module.<id>`);
3. the member's role and access profile allow the route's `access`
   (`read`, `write` or `admin`).

The plugin gets a `Session` with the account id. Every query it makes is
scoped to that account.

## Rules

- A plugin reaches the platform only through `plugin.Host`.
- A plugin serves only the routes its manifest declares. One more or one
  less and the platform refuses to start it.
- A plugin owns its storage and never reads another plugin's.
- A plugin that cannot run here (a missing capability, a platform outside
  its version range) is reported as `unavailable` with the reason. It never
  takes the platform down.

## Packages

| Package | For |
|---|---|
| [`manifest`](manifest) | `plugin.yaml`: types, parsing, validation, version ranges |
| [`plugin`](plugin) | `Plugin`, `Host`, `Session`, and the `Registry` the platform runs |
| [`plugintest`](plugintest) | The conformance suite and a fake platform for tests |

More: [the contract in detail](docs/contract.md).

## Status

`v0`: the contract can still change between minor versions until the first
plugins have moved onto it. Changes are listed in [CHANGELOG.md](CHANGELOG.md).

## Development

```sh
go test ./...
```

## License

Apache-2.0. See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).
