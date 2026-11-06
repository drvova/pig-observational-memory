//go:build race

package memeval

// raceEnabled shrinks the tests' default run under the race detector, which makes the engine
// about ten times slower.
const raceEnabled = true
