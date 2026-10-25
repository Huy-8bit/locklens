// Package buildinfo exposes version metadata injected at build time with
//
//	-ldflags "-X github.com/locklens/locklens/internal/lib/buildinfo.Version=v0.1.0
//	          -X github.com/locklens/locklens/internal/lib/buildinfo.Commit=abc1234
//	          -X github.com/locklens/locklens/internal/lib/buildinfo.Date=2026-10-24T00:00:00Z"
//
// When values are not injected, VCS data recorded by the Go toolchain is used
// if available.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Values set by -ldflags.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info is the resolved build metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
}

// Get returns the build metadata, falling back to embedded VCS settings.
func Get() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date, GoVersion: runtime.Version()}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}
	return info
}
