// Package plugintest is the conformance suite every nochaos plugin passes:
// one call in the plugin's own tests proves that its manifest is valid, that
// its code registers exactly what the manifest declares, and that it starts
// and stops cleanly. It also gives tests a fake platform to call the
// plugin's routes and run its jobs without a real one.
package plugintest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nochaosio/nochaos-sdk/manifest"
	"github.com/nochaosio/nochaos-sdk/plugin"
)

// Platform is a fake platform: it keeps what a plugin mounts so a test can
// call it. Zero value: version 0.1.0, no capability.
type Platform struct {
	// V is the platform version (default 0.1.0).
	V manifest.Version
	// Caps are the capabilities the installation offers.
	Caps []string

	mu     sync.Mutex
	routes map[string]mounted
	jobs   map[string]plugin.JobFunc
}

type mounted struct {
	route manifest.Route
	h     plugin.Handler
}

func (p *Platform) Version() manifest.Version {
	if p.V == (manifest.Version{}) {
		return manifest.Version{Minor: 1}
	}
	return p.V
}

func (p *Platform) Capabilities() []string { return p.Caps }

func (p *Platform) Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (p *Platform) Mount(_ string, r manifest.Route, h plugin.Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.routes == nil {
		p.routes = map[string]mounted{}
	}
	p.routes[r.Key()] = mounted{r, h}
}

func (p *Platform) Schedule(_ context.Context, _ string, j manifest.Job, fn plugin.JobFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.jobs == nil {
		p.jobs = map[string]plugin.JobFunc{}
	}
	p.jobs[j.Name] = fn
}

// Mounted tells whether a route ("GET /api/path") reached the platform.
func (p *Platform) Mounted(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.routes[key]
	return ok
}

// Scheduled tells whether a job reached the platform.
func (p *Platform) Scheduled(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.jobs[name]
	return ok
}

// Call runs a mounted route as the given session and returns the response.
// key is "GET /api/path" exactly as the manifest declares it; target is the
// URL to request (with its query), "" for the path of the key.
func (p *Platform) Call(t *testing.T, key, target string, s plugin.Session) *httptest.ResponseRecorder {
	t.Helper()
	p.mu.Lock()
	m, ok := p.routes[key]
	p.mu.Unlock()
	if !ok {
		t.Fatalf("route %q is not mounted", key)
	}
	if target == "" {
		target = m.route.Path
	}
	rec := httptest.NewRecorder()
	m.h(rec, httptest.NewRequest(m.route.Method, target, http.NoBody), s)
	return rec
}

// RunJob runs a scheduled job once.
func (p *Platform) RunJob(t *testing.T, name string) error {
	t.Helper()
	p.mu.Lock()
	fn, ok := p.jobs[name]
	p.mu.Unlock()
	if !ok {
		t.Fatalf("job %q is not scheduled", name)
	}
	return fn(t.Context())
}

// Conformance is the check every plugin runs in its tests:
//
//	func TestConformance(t *testing.T) { plugintest.Conformance(t, myplugin.New(), nil) }
//
// It fails when the manifest is invalid, when the plugin registers a route
// or a job its manifest does not declare (or the other way round), when it
// does not start on a platform that offers everything it requires, or when
// it does not stop. pf may be nil: a platform with the plugin's required
// capabilities and a version inside its range is built.
func Conformance(t *testing.T, p plugin.Plugin, pf *Platform) *Platform {
	t.Helper()
	m := p.Manifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if pf == nil {
		pf = &Platform{Caps: m.Requires.Capabilities}
	}
	if m.Requires.Nochaos != "" {
		if rng, err := manifest.ParseRange(m.Requires.Nochaos); err == nil && !rng.Allows(pf.Version()) {
			pf.V = lowest(rng)
		}
	}
	if len(m.Requires.Plugins) > 0 {
		t.Skipf("plugin %s requires other plugins (%v): run its conformance with them registered", m.ID, m.Requires.Plugins)
	}
	reg := plugin.NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
	st := reg.Start(t.Context(), pf)[0]
	if st.State != plugin.Running {
		t.Fatalf("plugin %s is %s after start: %s", m.ID, st.State, st.Reason)
	}
	t.Cleanup(func() {
		if err := reg.Stop(context.Background()); err != nil {
			t.Errorf("stop: %v", err)
		}
	})
	return pf
}

// lowest is a version a range allows: the smallest lower bound it names.
func lowest(r manifest.Range) manifest.Version {
	for _, c := range []manifest.Version{{Minor: 1}, {Major: 1}} {
		if r.Allows(c) {
			return c
		}
	}
	for major := range 10 {
		for minor := range 100 {
			if v := (manifest.Version{Major: major, Minor: minor}); r.Allows(v) {
				return v
			}
		}
	}
	return manifest.Version{Minor: 1}
}
