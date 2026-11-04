module github.com/drvova/pig-observational-memory/extensions/observational-memory

go 1.26

require (
	github.com/MichaelKinsy/PiG/extensions/sdk v0.5.1
	github.com/drvova/pig-observational-memory/memory v0.0.0
)

require github.com/traefik/yaegi v0.16.1 // indirect

// The memory engine is the sibling core module; PiG promotes this replace into its build.
replace github.com/drvova/pig-observational-memory/memory => ../../memory
