package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nochaosio/nochaos-sdk/manifest"
)

// Platform is what the platform implements so a registry can run plugins on
// it. The registry never mounts a route or schedules a job the plugin's
// manifest does not declare.
type Platform interface {
	// Version of the platform, checked against requires.nochaos.
	Version() manifest.Version
	// Capabilities the installation offers (metrics-store, logs-store...).
	Capabilities() []string
	// Mount serves a declared route. The platform wraps the handler with
	// sign-in, the plugin's flag and the route's access.
	Mount(pluginID string, route manifest.Route, h Handler)
	// Schedule runs a declared job at its interval until the context of
	// Start ends.
	Schedule(ctx context.Context, pluginID string, job manifest.Job, fn JobFunc)
	Logger() *slog.Logger
}

// Registry holds the plugins of a platform process and runs their lifecycle:
// register, start (in dependency order), stop (in reverse).
type Registry struct {
	mu      sync.Mutex
	order   []string // registration order
	plugins map[string]Plugin
	status  map[string]*Status
	started []string // start order, for Stop
}

// NewRegistry is an empty registry.
func NewRegistry() *Registry {
	return &Registry{plugins: map[string]Plugin{}, status: map[string]*Status{}}
}

// Register adds a plugin. Its manifest must be valid and its id unique.
func (r *Registry) Register(p Plugin) error {
	m := p.Manifest()
	if err := m.Validate(); err != nil {
		return fmt.Errorf("plugin %q: %w", m.ID, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.plugins[m.ID]; dup {
		return fmt.Errorf("plugin %q is registered twice", m.ID)
	}
	r.plugins[m.ID] = p
	r.status[m.ID] = &Status{Manifest: m, State: Registered}
	r.order = append(r.order, m.ID)
	return nil
}

// Start starts every registered plugin the installation can run, required
// plugins first. A plugin that cannot run is Unavailable with the reason; one
// that breaks its manifest or returns an error is Failed. Neither stops the
// others: one broken plugin must not take the platform down.
func (r *Registry) Start(ctx context.Context, pf Platform) []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.startOrder() {
		st := r.status[id]
		if st.State != Registered {
			continue
		}
		p := r.plugins[id]
		if reason := r.unavailable(st.Manifest, pf); reason != "" {
			st.State, st.Reason = Unavailable, reason
			pf.Logger().Info("plugin unavailable", "plugin", id, "reason", reason)
			continue
		}
		h := newHost(st.Manifest, pf)
		err := p.Start(ctx, h)
		if err == nil {
			err = h.check()
		}
		if err != nil {
			st.State, st.Reason = Failed, err.Error()
			pf.Logger().Error("plugin failed to start", "plugin", id, "error", err)
			continue
		}
		// Only now does anything reach the platform: a plugin that fails
		// its checks leaves no route and no job behind.
		h.commit(ctx)
		st.State, st.StartedAt = Running, time.Now().UTC()
		r.started = append(r.started, id)
		pf.Logger().Info("plugin started", "plugin", id, "version", st.Manifest.Version)
	}
	return r.statusesLocked()
}

// Stop stops the running plugins in reverse start order. It returns the
// errors of every plugin that failed to stop.
func (r *Registry) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for i := len(r.started) - 1; i >= 0; i-- {
		id := r.started[i]
		if err := r.plugins[id].Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q: %w", id, err))
		}
		r.status[id].State = Stopped
	}
	r.started = nil
	return errors.Join(errs...)
}

// Statuses lists every plugin with its state, in registration order.
func (r *Registry) Statuses() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statusesLocked()
}

// Running tells whether a plugin is running.
func (r *Registry) Running(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.status[id]
	return ok && st.State == Running
}

func (r *Registry) statusesLocked() []Status {
	out := make([]Status, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, *r.status[id])
	}
	return out
}

// unavailable is the reason the installation cannot run a plugin, or "".
func (r *Registry) unavailable(m manifest.Manifest, pf Platform) string {
	if m.Requires.Nochaos != "" {
		rng, _ := manifest.ParseRange(m.Requires.Nochaos) // validated at Register
		if !rng.Allows(pf.Version()) {
			return fmt.Sprintf("needs nochaos %s, this is %s", m.Requires.Nochaos, pf.Version())
		}
	}
	have := pf.Capabilities()
	var missing []string
	for _, c := range m.Requires.Capabilities {
		if !slices.Contains(have, c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return "this installation does not offer: " + strings.Join(missing, ", ")
	}
	for _, dep := range m.Requires.Plugins {
		st, ok := r.status[dep]
		if !ok {
			return "needs the plugin " + dep + ", which is not installed"
		}
		if st.State != Running {
			return "needs the plugin " + dep + ", which is " + string(st.State)
		}
	}
	return ""
}

// startOrder puts required plugins before the ones that need them, keeping
// registration order otherwise. A cycle keeps registration order: its
// members then report each other as not running.
func (r *Registry) startOrder() []string {
	var out []string
	done := map[string]bool{}
	visiting := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		if done[id] || visiting[id] {
			return
		}
		visiting[id] = true
		deps := append([]string(nil), r.status[id].Manifest.Requires.Plugins...)
		sort.Strings(deps)
		for _, d := range deps {
			if _, ok := r.plugins[d]; ok {
				visit(d)
			}
		}
		visiting[id] = false
		done[id] = true
		out = append(out, id)
	}
	for _, id := range r.order {
		visit(id)
	}
	return out
}

// host is the Host a plugin gets: it records what the plugin registers and
// compares it with the manifest before anything reaches the platform.
type host struct {
	m      manifest.Manifest
	pf     Platform
	routes map[string]Handler
	jobs   map[string]JobFunc
	errs   []string
}

func newHost(m manifest.Manifest, pf Platform) *host {
	return &host{m: m, pf: pf, routes: map[string]Handler{}, jobs: map[string]JobFunc{}}
}

func (h *host) Handle(method, path string, fn Handler) {
	key := method + " " + path
	switch {
	case fn == nil:
		h.errs = append(h.errs, key+": nil handler")
	case !slices.Contains(h.m.RouteKeys(), key):
		h.errs = append(h.errs, key+": not declared in the manifest")
	case h.routes[key] != nil:
		h.errs = append(h.errs, key+": registered twice")
	default:
		h.routes[key] = fn
	}
}

func (h *host) Every(name string, fn JobFunc) {
	switch {
	case fn == nil:
		h.errs = append(h.errs, "job "+name+": nil function")
	case !slices.Contains(h.m.JobNames(), name):
		h.errs = append(h.errs, "job "+name+": not declared in the manifest")
	case h.jobs[name] != nil:
		h.errs = append(h.errs, "job "+name+": registered twice")
	default:
		h.jobs[name] = fn
	}
}

func (h *host) Logger() *slog.Logger { return h.pf.Logger().With("plugin", h.m.ID) }

func (h *host) Capability(name string) bool { return slices.Contains(h.pf.Capabilities(), name) }

// check: the plugin registered exactly what its manifest declares.
func (h *host) check() error {
	errs := append([]string(nil), h.errs...)
	for _, k := range h.m.RouteKeys() {
		if h.routes[k] == nil {
			errs = append(errs, k+": declared in the manifest, never registered")
		}
	}
	for _, n := range h.m.JobNames() {
		if h.jobs[n] == nil {
			errs = append(errs, "job "+n+": declared in the manifest, never registered")
		}
	}
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return errors.New("the plugin does not match its manifest: " + strings.Join(errs, "; "))
}

func (h *host) commit(ctx context.Context) {
	for _, r := range h.m.API {
		h.pf.Mount(h.m.ID, r, h.routes[r.Key()])
	}
	for _, j := range h.m.Jobs {
		h.pf.Schedule(ctx, h.m.ID, j, h.jobs[j.Name])
	}
}
