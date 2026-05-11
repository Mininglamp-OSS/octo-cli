package cmd

// Build metadata. Set via -ldflags at release time.
//
//	go build -ldflags "-X github.com/Mininglamp-OSS/octo-cli/cmd.Version=v0.2.0 \
//	                   -X github.com/Mininglamp-OSS/octo-cli/cmd.Commit=<sha> \
//	                   -X github.com/Mininglamp-OSS/octo-cli/cmd.BuildDate=<date>"
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)
