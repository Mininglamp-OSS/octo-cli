package cmd

// Build metadata. Set via -ldflags at release time.
//
//	go build -ldflags "-X github.com/dmwork-org/octo-cli/cmd.Version=v0.2.0 \
//	                   -X github.com/dmwork-org/octo-cli/cmd.Commit=<sha> \
//	                   -X github.com/dmwork-org/octo-cli/cmd.BuildDate=<date>"
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)
