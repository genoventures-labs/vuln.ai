package taloscli

import "runtime"

var (
	// Set via -ldflags at build time when producing release binaries.
	talosVersion   = "dev"
	talosCommit    = "none"
	talosBuildDate = "unknown"
)

func currentTalosVersion() string {
	if talosVersion == "" {
		return "dev"
	}
	return talosVersion
}

func currentTalosCommit() string {
	if talosCommit == "" {
		return "none"
	}
	return talosCommit
}

func currentTalosBuildDate() string {
	if talosBuildDate == "" {
		return "unknown"
	}
	return talosBuildDate
}

func runtimeDescriptor() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
