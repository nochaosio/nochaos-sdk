// Package plugin is what a nochaos plugin implements and what the platform
// gives it. A plugin is a small program with one job (alerts, logs, the
// internet status...) that plugs into the platform's backbone: the backbone
// owns sign-in, accounts, permissions, feature flags and the menu; the
// plugin owns its routes, its data and its background work.
//
// The rules that keep plugins independent:
//
//   - a plugin reaches the platform only through Host;
//   - a plugin serves only the routes its manifest declares;
//   - the platform checks sign-in, the plugin's flag and the member's
//     permission before a handler runs, so a handler never does;
//   - a plugin keeps its state in its own storage and never reads another
//     plugin's.
package plugin

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/nochaosio/nochaos-sdk/manifest"
)

// Session is who is calling a route, already authenticated and authorized
// by the platform for this plugin and this route's access.
type Session struct {
	// AccountID is the account (tenant) the call is for. Every query a
	// plugin makes is scoped to it.
	AccountID string
	// UserID is empty for calls made with a key.
	UserID string
	// Role is owner, admin, member or viewer.
	Role string
	// Environments the call is limited to; empty means all of them.
	Environments []string
}

// Handler serves one route.
type Handler func(w http.ResponseWriter, r *http.Request, s Session)

// JobFunc is a background task. An error is logged by the platform; the job
// runs again on its next tick.
type JobFunc func(ctx context.Context) error

// Host is the platform, as a plugin sees it.
type Host interface {
	// Handle serves a route the manifest declares. Registering a route the
	// manifest does not declare, or with another access, fails the start.
	Handle(method, path string, h Handler)
	// Every runs a job the manifest declares, at its declared interval.
	Every(name string, fn JobFunc)
	// Logger writes to the platform's log, tagged with the plugin's id.
	Logger() *slog.Logger
	// Capability tells whether the installation offers a capability the
	// manifest marked optional. Required ones are checked before Start.
	Capability(name string) bool
}

// Plugin is what a plugin implements.
type Plugin interface {
	// Manifest is the plugin's declaration (usually its embedded
	// plugin.yaml, parsed once).
	Manifest() manifest.Manifest
	// Start registers the plugin's routes and jobs on the host. It must
	// not block: long work belongs in jobs.
	Start(ctx context.Context, h Host) error
	// Stop releases what Start acquired. It is called once, after the
	// platform stopped sending requests and jobs.
	Stop(ctx context.Context) error
}

// State of a plugin in a registry.
type State string

const (
	// Registered: known, not started yet.
	Registered State = "registered"
	// Running: started and serving.
	Running State = "running"
	// Unavailable: the installation cannot run it (a missing capability, a
	// platform version out of its range, a required plugin not running).
	// Not an error: the reason is shown to the operator.
	Unavailable State = "unavailable"
	// Failed: its Start returned an error or broke its manifest.
	Failed State = "failed"
	// Stopped: was running, stopped cleanly.
	Stopped State = "stopped"
)

// Status is a plugin and how it is doing.
type Status struct {
	Manifest manifest.Manifest `json:"manifest"`
	State    State             `json:"state"`
	// Reason says why it is unavailable or failed.
	Reason    string    `json:"reason,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
}
