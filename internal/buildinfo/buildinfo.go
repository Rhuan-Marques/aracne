// Package buildinfo carries the version string the binary was stamped with, for the surfaces
// that have to report it to something other than a person.
package buildinfo

// Version is the build version. main sets it from its own ldflags-injected value at startup,
// rather than being the -X target itself, so the documented build command and the release
// workflow keep pointing at main.Version.
//
// It exists because the MCP handshake used to answer `serverInfo.version` with a hardcoded
// "1.0.0" while the binary beside it reported a commit hash: a client logging the handshake
// could not tell which build it was talking to, and the literal would have become briefly true
// and then permanently wrong the moment a v1.0.0 tag existed.
var Version = "dev"
