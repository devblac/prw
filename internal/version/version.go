package version

import "fmt"

// These variables can be set at build time using ldflags.
var (
	Version = "0.2.0"
	Commit  = "unknown"
)

// String returns the full version string.
func String() string {
	if Commit != "unknown" && Commit != "" {
		// Safely truncate commit hash to 7 chars, or use full length if shorter
		commitShort := Commit
		if len(Commit) > 7 {
			commitShort = Commit[:7]
		}
		return fmt.Sprintf("%s (%s)", Version, commitShort)
	}
	return Version
}
