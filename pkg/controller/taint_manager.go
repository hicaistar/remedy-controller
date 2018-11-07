package controller


import (
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
)


type TaintManager struct {
	client clientset.Interface
	recorder record.EventRecorder
	eventCh chan string
}

// NewTaintManager creates a new TaintManager that will use passed clientset to
// communicate with the API server.
func NewTaintManager(c clientset.Interface) *TaintManager {
	eventBroadcaster := record.NewBroadcaster()
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "remedy-taint-controller"})
	eventBroadcaster.StartLogging(glog.Infof)
	if c != nil {
		glog.Infof("Sending events to api server")
		eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: c.CoreV1().Events("")})
	} else {
		glog.Fatalf("kubeClient is nil when starting RemedyController")
	}

	tm := &TaintManager{
		client:   c,
		recorder: recorder,
	}
	return tm
}

//
func (tm *TaintManager) Run(stopCh <-chan struct{}) {
	glog.Infof("Starting TaintManager")
	defer glog.Infof("Shutting down TaintManager")
	for {
		select {
		case event := <-tm.eventCh:
			glog.Infof("get event: %v", event)
		}
	}
}

// NodeUpdated is used to notify TaintManager about Node conditions' changes.
func (tm *TaintManager) NodeUpdated(oldNode *v1.Node, newNode *v1.Node) {
	newConditions := newNode.Status.Conditions
	// remove it.
	for _, c := range newConditions {
		glog.Infof("get condition: %v=%v", c.Type, c.Status)
	}
}

// NodeEventsAdded is used to notify TaintManager about Node Events add.
func (tm *TaintManager) NodeEventAdded(event *v1.Event) {
	// skip other events
	if event.Source.Component != "kubelet" || event.InvolvedObject.Kind != "Node" {
		return
	}
	glog.Infof("get node event from: %v, reason: %v, message: %v, type: %v\n",
		event.Name, event.Reason, event.Message, event.Type)
}