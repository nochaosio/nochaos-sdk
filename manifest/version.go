package manifest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Version is MAJOR.MINOR.PATCH. Pre-release and build suffixes are not
// supported on purpose: a plugin either is a release or is not installed.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compare is -1, 0 or 1 as v is older than, equal to or newer than o.
func (v Version) Compare(o Version) int {
	for _, d := range []int{v.Major - o.Major, v.Minor - o.Minor, v.Patch - o.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

// ParseVersion reads "1.2.3" (a leading v is accepted).
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", s)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return Version{}, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", s)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

// Range is a list of bounds that must all hold: ">=0.3.0 <1.0.0".
type Range []bound

type bound struct {
	op string
	v  Version
}

// ParseRange reads space-separated bounds with the operators >=, >, <=, <
// and =. Versions may omit the patch or the minor (">=0.3" is ">=0.3.0").
func ParseRange(s string) (Range, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty version range")
	}
	var out Range
	for _, f := range fields {
		op := ""
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(f, candidate) {
				op = candidate
				break
			}
		}
		if op == "" {
			return nil, fmt.Errorf("%q needs an operator (>=, >, <=, <, =)", f)
		}
		num := strings.TrimPrefix(f, op)
		for strings.Count(num, ".") < 2 {
			num += ".0"
		}
		v, err := ParseVersion(num)
		if err != nil {
			return nil, err
		}
		out = append(out, bound{op, v})
	}
	return out, nil
}

// Allows tells whether v satisfies every bound.
func (r Range) Allows(v Version) bool {
	for _, b := range r {
		c := v.Compare(b.v)
		ok := map[string]bool{">=": c >= 0, ">": c > 0, "<=": c <= 0, "<": c < 0, "=": c == 0}[b.op]
		if !ok {
			return false
		}
	}
	return true
}

func parseEvery(s string) (time.Duration, error) { return time.ParseDuration(s) }

// Every is the job's interval.
func (j Job) Interval() time.Duration {
	d, _ := parseEvery(j.Every)
	return d
}
