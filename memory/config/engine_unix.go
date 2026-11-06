//go:build unix

package config

// LispSupported reports whether the Lisp image has a transport on this platform (a unix socket
// and flock); a variable so tests can play another platform.
var LispSupported = true
