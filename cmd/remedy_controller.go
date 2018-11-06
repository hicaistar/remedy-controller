package main

import (
	"remedy-controller/cmd/options"
	"github.com/golang/glog"
	"remedy-controller/pkg/controller"
	"github.com/spf13/pflag"
)

func main() {
	rco := options.NewRemedyControllerOptions()
	pflag.Parse()
	glog.Infof("rco: %s", rco.PermanentConditions)

	c, err := controller.NewRemedyController()
	if err != nil {
		glog.Fatalf("Create remedy controller error: %v", err)
		return
	}

	c.Run()
}