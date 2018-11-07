package main

import (
	"time"
	"math/rand"
	"remedy-controller/cmd/options"
	"github.com/golang/glog"
	"remedy-controller/pkg/controller"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	rco := options.NewRemedyControllerOptions()

	//flag.Parse()

	glog.Infof("rco: %s", rco.PermanentConditions)

	c, err := controller.NewRemedyController()
	if err != nil {
		glog.Fatalf("Create remedy controller error: %v", err)
		return
	}

	c.Run()
}