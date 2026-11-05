// Package contracts holds only tests: they check the engine against the language-neutral
// contracts under the repository's contracts/ directory (contracts/README.md). The JSON
// Schemas there must validate every file and session entry the engine writes, the data memory
// v1 wrote included, and the golden corpus pins the engine's level-0 records and rendered
// compaction summary for each real transcript in memory/transcript/testdata/real, byte for
// byte. Regenerate the corpus with go test ./contracts -args -write.
package contracts
