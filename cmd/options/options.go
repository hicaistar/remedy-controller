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
	// GraceUncordonNodePeriod specifies the duration to uncordon node.
	GraceUncordonNodePeriod int32
}

func NewRemedyControllerOptions() *RemedyControllerOptions {
	return &RemedyControllerOptions{}
}

// AddFlags adds remedy controller command line options to pflag.
func (rco *RemedyControllerOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringSliceVar(&rco.MonitorConfigPaths, "monitors",
		[]string{}, "List of paths to monitor config files, comma separated.")
	fs.Int32Var(&rco.GraceUncordonNodePeriod, "uncordon-node-period",
		10, "Grace period of uncording node, for example, '10' means 10 Minutes.")
}

func init() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
}
