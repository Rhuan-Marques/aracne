// Package inits collects Go package-initialization edge cases: a package-level
// variable initialized by a LOCAL function call (var Config = loadConfig()) and
// a SINGLE init function. A second init is injected only by the at-scale
// duplicate-init scenario (never permanent corpus) so a possible duplicate-id
// write-abort cannot affect every scan.
package inits

// Config is a package-level var INITIALIZED BY A LOCAL CALL (loadConfig()).
var Config = loadConfig()

// ready is flipped by init below.
var ready bool

// loadConfig builds the default configuration; it is called from Config's
// initializer above.
func loadConfig() map[string]string {
	return map[string]string{"env": "test"}
}

// init runs at package load and marks the package ready — a SINGLE init
// function (id: ...inits.init).
func init() {
	ready = true
}

// Ready reports whether init has run.
func Ready() bool {
	return ready
}
