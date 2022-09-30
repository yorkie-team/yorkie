package version

// At build time, the versions is replaced with the current version using the -X linker flag
var (
	// Version is the main version number that is being run at the moment.
	Version = "0.0.0"

	// BuildDate is the date the executable was built.
	BuildDate string
)
