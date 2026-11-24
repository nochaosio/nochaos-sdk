# The plugin contract

## Lifecycle

```
register -> start -> running -> stop
              |-> unavailable (the installation cannot run it; reason shown)
              |-> failed      (it broke its manifest or returned an error)
```

- **register**: the manifest is validated. An invalid manifest or a
  duplicate id is refused.
- **start**: required plugins start first. The platform checks the version
  range, the capabilities and the required plugins; then calls `Start`; then
  compares what the plugin registered with its manifest. Only if everything
  matches are the routes mounted and the jobs scheduled. A plugin that fails
  leaves nothing behind.
- **stop**: reverse start order, once.

Enabling and disabling a plugin **for an account** is not part of this
lifecycle: it is the feature flag `module.<id>`, evaluated by the platform on
every request. A disabled plugin answers 404 for that account and its menu
items disappear; its data is kept.

## The manifest, field by field

| Field | Meaning |
|---|---|
| `apiVersion` | `nochaos.io/plugin/v1` |
| `id` | Lowercase, digits, dashes. Names the flag (`module.<id>`), the permission in access profiles, the folder |
| `name`, `summary` | Shown to people |
| `version` | `MAJOR.MINOR.PATCH` |
| `runtime` | `in-process` (default: compiled into the platform) or `service` (its own process) |
| `requires.nochaos` | Platform versions it runs on: `">=0.3 <1"` |
| `requires.capabilities` | What the installation must offer (`metrics-store`, `logs-store`, `traces-store`, `events-store`, `stream-aggregation`, `object-store`...) |
| `requires.plugins` | Plugins it builds on |
| `flag.default` | On or off for an account that has no rule. Ship off when the plugin opens a new way into an account's data |
| `api[]` | Every route: `method`, `path` (under `/api/`), `access` (`read`, `write`, `admin`) |
| `ui.nav[]` | Menu items: `group`, `path`, `label`, `icon` |
| `configKinds` | Configuration-as-code kinds it owns |
| `alertKinds` | Kinds of alert rule it evaluates |
| `mcpTools` | Tools it offers to AI clients |
| `jobs[]` | Background tasks: `name`, `every` |
| `storage.postgresSchema` | The schema that holds its tables |
| `tools` | The open-source tools it is built on (ids of the tools catalog) |
| `meters` | Usage counters it reports |

Unknown fields are an error.

## Capabilities: one software, many installations

The same plugin code runs on a single small machine and on a scaled cloud.
What differs is what the installation offers. A plugin declares the
capabilities it needs; an installation declares the ones it has. A plugin
whose capabilities are missing shows as unavailable in that installation,
with the reason, instead of failing at runtime.

## Compatibility promise

While the SDK is `v0`, a minor version may change the contract; every change
is in the changelog with what a plugin must do. From `v1`, a manifest and a
plugin that work keep working for the whole major version.
