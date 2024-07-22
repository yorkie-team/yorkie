package types

// VersionInfo represents information of version.
type VersionInfo struct {
	// ClientVersion is the yorkie cli version.
	ClientVersion *VersionDetail `json:"clientVersion,omitempty" yaml:"clientVersion,omitempty"`

	// ServerVersion is the yorkie server version.
	ServerVersion *VersionDetail `json:"serverVersion,omitempty" yaml:"serverVersion,omitempty"`
}

// VersionDetail represents detail information of version.
type VersionDetail struct {
	// YorkieVersion
	YorkieVersion string `json:"yorkieVersion" yaml:"yorkieVersion"`

	// GoVersion
	GoVersion string `json:"goVersion" yaml:"goVersion"`

	// BuildDate
	BuildDate string `json:"buildDate" yaml:"buildDate"`
}
