package manifest

import (
	"strings"
	"testing"
)

const good = `
apiVersion: nochaos.io/plugin/v1
id: internet-status
name: Internet status
version: 0.1.0
summary: Official status of the services teams depend on.
requires:
  nochaos: ">=0.1 <1"
flag:
  default: true
api:
  - {method: GET, path: /api/internet-status, access: read}
jobs:
  - {name: poll, every: 2m}
ui:
  nav: []
tools: []
`

func TestParseGoodManifest(t *testing.T) {
	m, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "internet-status" || m.Runtime != RuntimeInProcess || m.RouteKeys()[0] != "GET /api/internet-status" || m.Jobs[0].Interval().Minutes() != 2 {
		t.Fatalf("%+v", m)
	}
}

// A typo in a manifest is an error, never a field silently ignored.
func TestUnknownFieldIsRefused(t *testing.T) {
	if _, err := Parse([]byte(good + "permisions: [x]\n")); err == nil || !strings.Contains(err.Error(), "permisions") {
		t.Fatalf("want an unknown field error, got %v", err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	m := Manifest{APIVersion: "v0", ID: "Bad_ID", Version: "1.0", Runtime: "magic",
		Requires: Requires{Nochaos: "newest", Capabilities: []string{"Metrics Store"}, Plugins: []string{"Bad_ID"}},
		API: []Route{
			{Method: "GET", Path: "/internal/x", Access: "read"},
			{Method: "FETCH", Path: "/api/x", Access: "root"},
			{Method: "GET", Path: "/api/y", Access: "write"},
			{Method: "POST", Path: "/api/z", Access: "write"}, {Method: "POST", Path: "/api/z", Access: "write"},
		},
		UI:          UI{Nav: []NavItem{{Group: "Respond", Path: "alerts", Label: ""}}},
		ConfigKinds: []string{"alertRule"}, AlertKinds: []string{"Bad-Kind"},
		Jobs:    []Job{{Name: "poll", Every: "soon"}, {Name: "poll", Every: "1m"}},
		Storage: Storage{PostgresSchema: "Plugin-X"}, Tools: []string{"Victoria Metrics"},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("want errors")
	}
	for _, want := range []string{"apiVersion must be", "id \"Bad_ID\"", "name is required", "version:", "runtime must be", "requires.nochaos",
		"requires.capabilities", "path must start with /api/", "method must be", "access must be", "a GET never changes data", "declared twice",
		"group must be", "must start with /", "label is required", "must be CamelCase", "lowercase with underscores", "not a positive duration",
		"storage.postgresSchema", "not a tool id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}

func TestVersionsAndRanges(t *testing.T) {
	v, err := ParseVersion("v0.4.2")
	if err != nil || v.String() != "0.4.2" {
		t.Fatalf("%v %v", v, err)
	}
	for _, bad := range []string{"1", "1.2", "a.b.c", "1.2.-3", ""} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
	r, err := ParseRange(">=0.3 <1")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{"0.2.9": false, "0.3.0": true, "0.9.9": true, "1.0.0": false}
	for s, want := range cases {
		v, _ := ParseVersion(s)
		if r.Allows(v) != want {
			t.Errorf("%s in >=0.3 <1: got %v", s, !want)
		}
	}
	for _, bad := range []string{"", "0.3", "~0.3", ">=x"} {
		if _, err := ParseRange(bad); err == nil {
			t.Errorf("range %q should not parse", bad)
		}
	}
}
