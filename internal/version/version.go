package version

// Version is set at build time via -ldflags="-X github.com/stanier/stoa/internal/version.Version=<value>".
// Falls back to "dev" when built without the flag (debug builds, go run, etc.).
var Version = "dev"
