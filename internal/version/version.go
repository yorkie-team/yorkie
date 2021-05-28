package version

// At build time, the versions is replaced with the current version using the -X linker flag
var (
	// The git commit that was compiled.
	GitCommit string

	// The main version number that is being run at the moment.
	Version = "0.0.0"

	// The date the executable was built.
	BuildDate string
)
