package options

import (
	"flag"
	"github.com/spf13/pflag"
)

type RemedyControllerOptions struct {
	// command line options

	// MonitorConfigPaths specifies the list of paths to system log monitor configuration
	// files.
	MonitorConfigPaths []string
}

func NewRemedyControllerOptions() *RemedyControllerOptions {
	return &RemedyControllerOptions{}
}

// AddFlags adds remedy controller command line options to pflag.
func (rco *RemedyControllerOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringSliceVar(&rco.MonitorConfigPaths, "monitors",
		[]string{}, "List of paths to monitor config files, comma separated.")
}

func init() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
}
