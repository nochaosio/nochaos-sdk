// Package manifest is the contract a nochaos plugin declares: who it is,
// what it needs from the platform, and everything it adds to it (API
// routes, menu items, configuration kinds, alert kinds, MCP tools, jobs).
//
// The platform reads the manifest before running any plugin code, and
// refuses a plugin whose code does not match what it declared. A manifest is
// a YAML file named plugin.yaml next to the plugin's code.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// APIVersion is the manifest version this SDK reads.
const APIVersion = "nochaos.io/plugin/v1"

// Runtimes a plugin can ask for.
const (
	RuntimeInProcess = "in-process" // compiled into the platform binary
	RuntimeService   = "service"    // its own process, reached over the network
)

// Access a route needs from the member calling it. The platform enforces it
// before the plugin's handler runs.
const (
	AccessRead  = "read"  // any member who can see the plugin
	AccessWrite = "write" // members who can change the plugin's data
	AccessAdmin = "admin" // account admins only
)

// Manifest is the content of plugin.yaml.
type Manifest struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	// ID names the plugin everywhere: its feature flag (module.<id>), its
	// permission in access profiles, its folder. Lowercase, digits, dashes.
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
	Summary string `yaml:"summary" json:"summary"`
	// Runtime is in-process (default) or service.
	Runtime  string   `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Requires Requires `yaml:"requires,omitempty" json:"requires"`
	// Flag says whether a new account gets the plugin without asking.
	Flag Flag `yaml:"flag,omitempty" json:"flag"`
	// API lists every route the plugin serves. Nothing else is reachable.
	API []Route `yaml:"api,omitempty" json:"api"`
	UI  UI      `yaml:"ui,omitempty" json:"ui"`
	// ConfigKinds are the configuration-as-code kinds it owns (AlertRule...).
	ConfigKinds []string `yaml:"configKinds,omitempty" json:"configKinds"`
	// AlertKinds are the kinds of alert rule it evaluates.
	AlertKinds []string `yaml:"alertKinds,omitempty" json:"alertKinds"`
	// MCPTools are the tools it offers to AI clients.
	MCPTools []string `yaml:"mcpTools,omitempty" json:"mcpTools"`
	Jobs     []Job    `yaml:"jobs,omitempty" json:"jobs"`
	Storage  Storage  `yaml:"storage,omitempty" json:"storage"`
	// Tools are the open-source tools the plugin is built on (ids of the
	// tools catalog: victoriametrics, clickhouse, grafana...). nochaos is a
	// layer above them and says so.
	Tools []string `yaml:"tools,omitempty" json:"tools"`
	// Meters are the usage counters it reports (billing reads them in the
	// cloud edition; self-hosted installs ignore them).
	Meters []string `yaml:"meters,omitempty" json:"meters"`
}

// Requires is what the plugin needs to run.
type Requires struct {
	// Nochaos is a range of platform versions: ">=0.3.0 <1.0.0".
	Nochaos string `yaml:"nochaos,omitempty" json:"nochaos,omitempty"`
	// Capabilities the installation must offer (metrics-store, logs-store,
	// stream-aggregation...). A single-machine install offers fewer than the
	// cloud; a plugin whose capabilities are missing is reported as
	// unavailable instead of failing at runtime.
	Capabilities []string `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	// Plugins it builds on (ids).
	Plugins []string `yaml:"plugins,omitempty" json:"plugins,omitempty"`
}

// Flag is the plugin's feature flag.
type Flag struct {
	// Default is the state when no rule says otherwise. Plugins that open a
	// new way into an account's data should ship off.
	Default bool `yaml:"default" json:"default"`
}

// Route is one API route of the plugin.
type Route struct {
	Method string `yaml:"method" json:"method"`
	// Path starts with /api/. It may hold {name} segments.
	Path string `yaml:"path" json:"path"`
	// Access is read, write or admin.
	Access string `yaml:"access" json:"access"`
}

// Key is how a route is compared: "GET /api/x".
func (r Route) Key() string { return r.Method + " " + r.Path }

// UI is what the plugin adds to the portal.
type UI struct {
	Nav []NavItem `yaml:"nav,omitempty" json:"nav"`
}

// NavItem is one page in the menu.
type NavItem struct {
	// Group is the menu group id (respond, apm, explore...).
	Group string `yaml:"group" json:"group"`
	Path  string `yaml:"path" json:"path"`
	Label string `yaml:"label" json:"label"`
	Icon  string `yaml:"icon,omitempty" json:"icon,omitempty"`
}

// Job is a background task the platform runs for the plugin.
type Job struct {
	Name string `yaml:"name" json:"name"`
	// Every is a Go duration: 30s, 2m, 1h.
	Every string `yaml:"every" json:"every"`
}

// Storage is the state the plugin owns.
type Storage struct {
	// PostgresSchema is the schema that holds its tables. A plugin never
	// reads another plugin's schema.
	PostgresSchema string `yaml:"postgresSchema,omitempty" json:"postgresSchema,omitempty"`
}

// Load reads and validates a plugin.yaml file.
func Load(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	m, err := Parse(b)
	if err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Parse reads and validates a manifest. Unknown fields are an error: a typo
// in a manifest must not be silently ignored.
func Parse(b []byte) (Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("invalid manifest: %w", err)
	}
	if m.Runtime == "" {
		m.Runtime = RuntimeInProcess
	}
	return m, m.Validate()
}

var (
	idRe      = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	kindRe    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]+$`)
	nameRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	pathRe    = regexp.MustCompile(`^/api(/([a-z0-9._-]+|\{[a-z_]+\}))+$`)
	navPathRe = regexp.MustCompile(`^/[a-z0-9/-]*$`)
	methods   = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
)

// Validate reports every problem of the manifest at once, so an author fixes
// them in one pass.
func (m Manifest) Validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if m.APIVersion != APIVersion {
		add("apiVersion must be %s", APIVersion)
	}
	if !idRe.MatchString(m.ID) || len(m.ID) > 40 {
		add("id %q: lowercase letters, digits and dashes, starting with a letter, at most 40 characters", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		add("name is required")
	}
	if _, err := ParseVersion(m.Version); err != nil {
		add("version: %v", err)
	}
	if m.Runtime != RuntimeInProcess && m.Runtime != RuntimeService {
		add("runtime must be %s or %s", RuntimeInProcess, RuntimeService)
	}
	if m.Requires.Nochaos != "" {
		if _, err := ParseRange(m.Requires.Nochaos); err != nil {
			add("requires.nochaos: %v", err)
		}
	}
	for _, c := range m.Requires.Capabilities {
		if !idRe.MatchString(c) {
			add("requires.capabilities: %q is not a capability id", c)
		}
	}
	for _, p := range m.Requires.Plugins {
		if !idRe.MatchString(p) {
			add("requires.plugins: %q is not a plugin id", p)
		}
		if p == m.ID {
			add("requires.plugins: a plugin cannot require itself")
		}
	}
	seen := map[string]bool{}
	for _, r := range m.API {
		if !methods[r.Method] {
			add("api %s: method must be GET, POST, PUT, PATCH or DELETE", r.Key())
		}
		if !pathRe.MatchString(r.Path) {
			add("api %s: path must start with /api/ and use lowercase segments or {name}", r.Key())
		}
		if r.Access != AccessRead && r.Access != AccessWrite && r.Access != AccessAdmin {
			add("api %s: access must be read, write or admin", r.Key())
		}
		if r.Method == "GET" && r.Access == AccessWrite {
			add("api %s: a GET never changes data, so its access is read or admin", r.Key())
		}
		if seen[r.Key()] {
			add("api %s: declared twice", r.Key())
		}
		seen[r.Key()] = true
	}
	for _, n := range m.UI.Nav {
		if !idRe.MatchString(n.Group) {
			add("ui.nav %q: group must be a menu group id", n.Label)
		}
		if !navPathRe.MatchString(n.Path) {
			add("ui.nav %q: path %q must start with /", n.Label, n.Path)
		}
		if strings.TrimSpace(n.Label) == "" {
			add("ui.nav %s: label is required", n.Path)
		}
	}
	for _, k := range m.ConfigKinds {
		if !kindRe.MatchString(k) {
			add("configKinds: %q must be CamelCase", k)
		}
	}
	for _, list := range [][]string{m.AlertKinds, m.MCPTools, m.Meters} {
		for _, n := range list {
			if !nameRe.MatchString(n) {
				add("%q must be lowercase with underscores", n)
			}
		}
	}
	for _, t := range m.Tools {
		if !idRe.MatchString(t) {
			add("tools: %q is not a tool id", t)
		}
	}
	jobs := map[string]bool{}
	for _, j := range m.Jobs {
		if !idRe.MatchString(j.Name) {
			add("jobs: name %q must be lowercase with dashes", j.Name)
		}
		if d, err := parseEvery(j.Every); err != nil || d <= 0 {
			add("jobs %s: every %q is not a positive duration (30s, 2m, 1h)", j.Name, j.Every)
		}
		if jobs[j.Name] {
			add("jobs %s: declared twice", j.Name)
		}
		jobs[j.Name] = true
	}
	if s := m.Storage.PostgresSchema; s != "" && !nameRe.MatchString(s) {
		add("storage.postgresSchema %q must be lowercase with underscores", s)
	}
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return errors.New(strings.Join(errs, "; "))
}

// RouteKeys is every declared route as "METHOD /path", sorted.
func (m Manifest) RouteKeys() []string {
	out := make([]string, 0, len(m.API))
	for _, r := range m.API {
		out = append(out, r.Key())
	}
	sort.Strings(out)
	return out
}

// JobNames is every declared job, sorted.
func (m Manifest) JobNames() []string {
	out := make([]string, 0, len(m.Jobs))
	for _, j := range m.Jobs {
		out = append(out, j.Name)
	}
	sort.Strings(out)
	return out
}
