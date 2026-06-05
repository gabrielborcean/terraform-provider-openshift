package provider

import (
	"fmt"
	"strconv"
	"strings"
)

// OCPCompat describes provider compatibility for a given OCP version.
type OCPCompat struct {
	OCPVersion     string
	MinProviderVer string
	K8sAPIVersions []string
	MinOCMirror    string
	Broken         bool
	BrokenReason   string
}

// CompatMatrix maps OCP versions to their compatibility metadata.
var CompatMatrix = []OCPCompat{
	{OCPVersion: "4.12", MinProviderVer: "0.1.0", K8sAPIVersions: []string{"1.25"}, MinOCMirror: "4.12.0"},
	{OCPVersion: "4.13", MinProviderVer: "0.1.0", K8sAPIVersions: []string{"1.26"}, MinOCMirror: "4.13.0"},
	{OCPVersion: "4.14", MinProviderVer: "0.1.0", K8sAPIVersions: []string{"1.27"}, MinOCMirror: "4.14.0"},
	{OCPVersion: "4.15", MinProviderVer: "0.1.0", K8sAPIVersions: []string{"1.28"}, MinOCMirror: "4.15.0"},
	{OCPVersion: "4.16", MinProviderVer: "0.1.0", K8sAPIVersions: []string{"1.29"}, MinOCMirror: "4.16.0"},
	{OCPVersion: "4.17", MinProviderVer: "0.1.0", K8sAPIVersions: []string{"1.30"}, MinOCMirror: "4.17.0"},
}

// LookupCompat returns the OCPCompat entry for the given OCP version (major.minor match).
// ocpVersion may be a full version like "4.14.37" or just "4.14".
func LookupCompat(ocpVersion string) (*OCPCompat, bool) {
	majorMinor := majorMinorOnly(ocpVersion)
	for i := range CompatMatrix {
		if CompatMatrix[i].OCPVersion == majorMinor {
			return &CompatMatrix[i], true
		}
	}
	return nil, false
}

// SupportedOCPVersions returns the list of OCP major.minor versions in the matrix.
func SupportedOCPVersions() []string {
	versions := make([]string, len(CompatMatrix))
	for i, c := range CompatMatrix {
		versions[i] = c.OCPVersion
	}
	return versions
}

// CheckCompat validates that ocpVersion and providerVersion are compatible.
// Returns an error for known-broken combinations or if the provider version is too old.
// Returns an *UnknownVersionError (which IsUnknownVersion identifies) when the OCP
// version is newer than any entry in the matrix — callers should emit a warning, not fail.
func CheckCompat(ocpVersion, providerVersion string) error {
	compat, found := LookupCompat(ocpVersion)
	if !found {
		return &UnknownVersionError{OCPVersion: ocpVersion}
	}

	if compat.Broken {
		return fmt.Errorf("OCP version %s is known broken with this provider: %s", ocpVersion, compat.BrokenReason)
	}

	// Compare provider version numerically.
	if providerVersion != "" && providerVersion != "dev" {
		pv, err := parseVersionTuple(providerVersion)
		if err == nil {
			minPV, minErr := parseVersionTuple(compat.MinProviderVer)
			if minErr == nil && versionLess(pv, minPV) {
				return fmt.Errorf(
					"OCP version %s requires provider >= %s, but running %s",
					ocpVersion, compat.MinProviderVer, providerVersion,
				)
			}
		}
	}

	return nil
}

// UnknownVersionError is returned by CheckCompat when the OCP version is not in the matrix.
// This is a soft error — callers should warn rather than fail hard.
type UnknownVersionError struct {
	OCPVersion string
}

func (e *UnknownVersionError) Error() string {
	return fmt.Sprintf(
		"OCP version %s is newer than the compatibility matrix (newest known: %s); "+
			"proceeding, but compatibility is not guaranteed",
		e.OCPVersion, newestKnown(),
	)
}

// IsUnknownVersion returns true if err is an *UnknownVersionError.
func IsUnknownVersion(err error) bool {
	_, ok := err.(*UnknownVersionError)
	return ok
}

// newestKnown returns the last OCP version in the matrix.
func newestKnown() string {
	if len(CompatMatrix) == 0 {
		return "unknown"
	}
	return CompatMatrix[len(CompatMatrix)-1].OCPVersion
}

// majorMinorOnly reduces "4.14.37" → "4.14".
func majorMinorOnly(v string) string {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

// versionTuple holds a parsed major.minor.patch triple.
type versionTuple [3]int

func parseVersionTuple(v string) (versionTuple, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var t versionTuple
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return t, fmt.Errorf("invalid version segment %q in %q: %w", parts[i], v, err)
		}
		t[i] = n
	}
	return t, nil
}

// versionLess returns true if a < b.
func versionLess(a, b versionTuple) bool {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return false
}
