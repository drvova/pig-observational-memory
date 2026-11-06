//go:build !unix

package config

// LispSupported reports whether the Lisp image has a transport on this platform: not here
// (sb-bsd-sockets has no local sockets on win32), so the Go engine stays the default for the
// first release in which the image is (docs/lisp-core.md, decision 10). A variable so tests can
// play another platform.
var LispSupported = false
