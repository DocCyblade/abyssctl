package buildinfo

import (
	"fmt"
	"strings"
)

// semver is a parsed SemVer 2.0 version. Numbers stay decimal strings, so no size limit applies.
type semver struct {
	core [3]string
	pre  []string // nil for a release
}

// parseSemver parses MAJOR.MINOR.PATCH[-prerelease][+build] with no leading "v".
func parseSemver(v string) (semver, bool) {
	var s semver
	if i := strings.IndexByte(v, '+'); i >= 0 {
		for _, id := range strings.Split(v[i+1:], ".") {
			if !alphanumeric(id) {
				return semver{}, false
			}
		}
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		s.pre = strings.Split(v[i+1:], ".")
		for _, id := range s.pre {
			if !alphanumeric(id) || (numeric(id) && !noLeadingZero(id)) {
				return semver{}, false
			}
		}
		v = v[:i]
	}
	if !PlainVersion(v) {
		return semver{}, false
	}
	copy(s.core[:], strings.Split(v, "."))
	return s, true
}

// alphanumeric reports whether id is a non-empty run of [0-9A-Za-z-].
func alphanumeric(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
		default:
			return false
		}
	}
	return true
}

// numeric reports whether id is a non-empty run of decimal digits.
func numeric(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func noLeadingZero(n string) bool { return n == "0" || n[0] != '0' }

// ValidVersion reports whether v is SemVer 2.0 without a leading "v": MAJOR.MINOR.PATCH[-prerelease][+build].
func ValidVersion(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

// CompareVersions returns -1, 0 or +1 as a sorts before, with or after b under SemVer 2.0 precedence: a
// pre-release sorts before its release and build metadata is ignored. It is an error if either is not valid.
func CompareVersions(a, b string) (int, error) {
	va, ok := parseSemver(a)
	if !ok {
		return 0, fmt.Errorf("version %q is not valid SemVer", a)
	}
	vb, ok := parseSemver(b)
	if !ok {
		return 0, fmt.Errorf("version %q is not valid SemVer", b)
	}
	for i := range va.core {
		if c := compareNumbers(va.core[i], vb.core[i]); c != 0 {
			return c, nil
		}
	}
	switch {
	case va.pre == nil && vb.pre == nil:
		return 0, nil
	case va.pre == nil:
		return 1, nil
	case vb.pre == nil:
		return -1, nil
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		x, y := va.pre[i], vb.pre[i]
		nx, ny := numeric(x), numeric(y)
		var c int
		switch {
		case nx && ny:
			c = compareNumbers(x, y)
		case nx:
			c = -1
		case ny:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c, nil
		}
	}
	switch {
	case len(va.pre) < len(vb.pre):
		return -1, nil
	case len(va.pre) > len(vb.pre):
		return 1, nil
	}
	return 0, nil
}

// compareNumbers compares two decimal strings without leading zeros.
func compareNumbers(x, y string) int {
	if len(x) != len(y) {
		if len(x) < len(y) {
			return -1
		}
		return 1
	}
	return strings.Compare(x, y)
}

// PlainVersion reports whether s is a plain stable version: MAJOR.MINOR.PATCH, three decimal numbers with no
// leading zeros, no "v", no pre-release and no build part.
func PlainVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if !numeric(p) || !noLeadingZero(p) {
			return false
		}
	}
	return true
}

// CheckStamped returns an error if Version is Unstamped or not valid SemVer.
func CheckStamped() error {
	if Version == Unstamped {
		return fmt.Errorf("version %s is the unstamped default", Version)
	}
	if !ValidVersion(Version) {
		return fmt.Errorf("version %q is not valid SemVer", Version)
	}
	return nil
}
