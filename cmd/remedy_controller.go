package main

import (
	"math/rand"
	"time"

	"github.com/golang/glog"
	"github.com/spf13/pflag"

	"remedy-controller/cmd/options"
	"remedy-controller/pkg/controller"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	rco := options.NewRemedyControllerOptions()

	rco.AddFlags(pflag.CommandLine)
	pflag.Parse()

	c, err := controller.NewRemedyController(rco)
	if err != nil {
		glog.Fatalf("Create remedy controller error: %v", err)
		return
	}

	c.Run()
}
