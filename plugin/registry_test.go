package plugin_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/nochaosio/nochaos-sdk/manifest"
	"github.com/nochaosio/nochaos-sdk/plugin"
	"github.com/nochaosio/nochaos-sdk/plugintest"
)

// fake is a plugin whose manifest and behaviour a test sets.
type fake struct {
	m       manifest.Manifest
	start   func(h plugin.Host) error
	stopped *[]string
}

func (f *fake) Manifest() manifest.Manifest { return f.m }
func (f *fake) Start(_ context.Context, h plugin.Host) error {
	if f.start != nil {
		return f.start(h)
	}
	for _, r := range f.m.API {
		h.Handle(r.Method, r.Path, func(w http.ResponseWriter, _ *http.Request, s plugin.Session) { _, _ = w.Write([]byte(s.AccountID)) })
	}
	for _, j := range f.m.Jobs {
		h.Every(j.Name, func(context.Context) error { return nil })
	}
	return nil
}
func (f *fake) Stop(context.Context) error {
	if f.stopped != nil {
		*f.stopped = append(*f.stopped, f.m.ID)
	}
	return nil
}

func mk(id string, edit func(*manifest.Manifest)) *fake {
	m := manifest.Manifest{APIVersion: manifest.APIVersion, ID: id, Name: id, Version: "0.1.0", Runtime: manifest.RuntimeInProcess,
		API:  []manifest.Route{{Method: "GET", Path: "/api/" + id, Access: manifest.AccessRead}},
		Jobs: []manifest.Job{{Name: "tick", Every: "1m"}}}
	if edit != nil {
		edit(&m)
	}
	return &fake{m: m}
}

func states(list []plugin.Status) map[string]plugin.State {
	out := map[string]plugin.State{}
	for _, s := range list {
		out[s.Manifest.ID] = s.State
	}
	return out
}

func TestStartMountsOnlyWhatTheManifestDeclares(t *testing.T) {
	pf := &plugintest.Platform{}
	reg := plugin.NewRegistry()
	if err := reg.Register(mk("alpha", nil)); err != nil {
		t.Fatal(err)
	}
	st := reg.Start(t.Context(), pf)
	if st[0].State != plugin.Running || st[0].StartedAt.IsZero() || !reg.Running("alpha") {
		t.Fatalf("%+v", st[0])
	}
	if got := pf.Call(t, "GET /api/alpha", "", plugin.Session{AccountID: "acme"}).Body.String(); got != "acme" {
		t.Fatalf("handler got session %q", got)
	}
	if err := pf.RunJob(t, "tick"); err != nil {
		t.Fatal(err)
	}
}

// Code and manifest must agree: a route or job on one side only fails the
// plugin, and nothing of it reaches the platform.
func TestPluginThatBreaksItsManifestFails(t *testing.T) {
	cases := map[string]func(h plugin.Host) error{
		"not declared in the manifest": func(h plugin.Host) error {
			h.Handle("GET", "/api/beta", func(http.ResponseWriter, *http.Request, plugin.Session) {})
			h.Handle("POST", "/api/secret", func(http.ResponseWriter, *http.Request, plugin.Session) {})
			h.Every("tick", func(context.Context) error { return nil })
			return nil
		},
		"never registered": func(h plugin.Host) error { return nil },
		"registered twice": func(h plugin.Host) error {
			for range 2 {
				h.Handle("GET", "/api/beta", func(http.ResponseWriter, *http.Request, plugin.Session) {})
			}
			h.Every("tick", func(context.Context) error { return nil })
			return nil
		},
		"boom": func(plugin.Host) error { return errors.New("boom") },
	}
	for want, start := range cases {
		pf := &plugintest.Platform{}
		reg := plugin.NewRegistry()
		p := mk("beta", nil)
		p.start = start
		_ = reg.Register(p)
		st := reg.Start(t.Context(), pf)[0]
		if st.State != plugin.Failed || !strings.Contains(st.Reason, want) {
			t.Errorf("%s: %s %q", want, st.State, st.Reason)
		}
		if pf.Mounted("GET /api/beta") || pf.Scheduled("tick") {
			t.Errorf("%s: a failed plugin left a route or a job on the platform", want)
		}
	}
}

func TestUnavailableIsNotAnError(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(mk("needs-stream", func(m *manifest.Manifest) { m.Requires.Capabilities = []string{"stream-aggregation"} }))
	_ = reg.Register(mk("too-new", func(m *manifest.Manifest) { m.Requires.Nochaos = ">=9.0" }))
	_ = reg.Register(mk("needs-ghost", func(m *manifest.Manifest) { m.Requires.Plugins = []string{"ghost"} }))
	_ = reg.Register(mk("fine", nil))
	got := reg.Start(t.Context(), &plugintest.Platform{Caps: []string{"metrics-store"}})
	want := map[string]plugin.State{"needs-stream": plugin.Unavailable, "too-new": plugin.Unavailable, "needs-ghost": plugin.Unavailable, "fine": plugin.Running}
	for id, s := range want {
		if states(got)[id] != s {
			t.Errorf("%s: %s, want %s", id, states(got)[id], s)
		}
	}
	for _, s := range got {
		if s.State == plugin.Unavailable && s.Reason == "" {
			t.Errorf("%s: unavailable without a reason", s.Manifest.ID)
		}
	}
}

func TestDependenciesStartFirstAndStopLast(t *testing.T) {
	var stopped []string
	reg := plugin.NewRegistry()
	child := mk("child", func(m *manifest.Manifest) { m.Requires.Plugins = []string{"base"} })
	base := mk("base", nil)
	child.stopped, base.stopped = &stopped, &stopped
	_ = reg.Register(child) // registered before what it needs
	_ = reg.Register(base)
	got := states(reg.Start(t.Context(), &plugintest.Platform{}))
	if got["base"] != plugin.Running || got["child"] != plugin.Running {
		t.Fatalf("%v", got)
	}
	if err := reg.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(stopped, ",") != "child,base" {
		t.Fatalf("stop order %v", stopped)
	}
}

func TestRegisterRefusesInvalidAndDuplicate(t *testing.T) {
	reg := plugin.NewRegistry()
	if err := reg.Register(mk("Bad", nil)); err == nil {
		t.Fatal("an invalid manifest must not register")
	}
	_ = reg.Register(mk("one", nil))
	if err := reg.Register(mk("one", nil)); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestConformancePassesForAGoodPlugin(t *testing.T) {
	pf := plugintest.Conformance(t, mk("good", func(m *manifest.Manifest) {
		m.Requires = manifest.Requires{Nochaos: ">=0.3 <1", Capabilities: []string{"logs-store"}}
	}), nil)
	if pf.Call(t, "GET /api/good", "", plugin.Session{AccountID: "a"}).Code != http.StatusOK {
		t.Fatal("route not callable")
	}
}
