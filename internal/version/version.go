// Package version exposes the prompto release version as a single
// variable so release builds can inject the tag with linker -X.
package version

// Version is the prompto release version. Bump on each release.
var Version = "0.1.0"
