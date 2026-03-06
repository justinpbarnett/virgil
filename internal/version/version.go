package version

import (
	"fmt"
	"runtime"
	"time"
)

var (
	// Set by build process via ldflags
	version   = "0.1.0-dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

// VersionInfo holds version metadata
type VersionInfo struct {
	Version   string
	GitCommit string
	BuildTime string
	GoVersion string
	Platform  string
}

// Version returns the version string
func Version() string {
	return version
}

// Info returns complete version information
func Info() *VersionInfo {
	return &VersionInfo{
		Version:   version,
		GitCommit: gitCommit,
		BuildTime: buildTime,
		GoVersion: runtime.Version(),
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// FullVersion returns a detailed version string
func FullVersion() string {
	info := Info()
	if info.GitCommit == "unknown" && info.BuildTime == "unknown" {
		return fmt.Sprintf("virgil %s (%s %s)", info.Version, info.GoVersion, info.Platform)
	}
	
	var buildTimeFormatted string
	if t, err := time.Parse(time.RFC3339, info.BuildTime); err == nil {
		buildTimeFormatted = t.Format("2006-01-02 15:04:05")
	} else {
		buildTimeFormatted = info.BuildTime
	}
	
	return fmt.Sprintf("virgil %s (commit: %s, built: %s, %s %s)",
		info.Version, info.GitCommit[:min(8, len(info.GitCommit))], buildTimeFormatted,
		info.GoVersion, info.Platform)
}

// UserAgent returns a user agent string for HTTP requests
func UserAgent() string {
	return fmt.Sprintf("virgil/%s", version)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}