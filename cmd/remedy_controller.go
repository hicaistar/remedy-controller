package main

import (
	"time"
	"math/rand"
	"github.com/spf13/pflag"
	"github.com/golang/glog"

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