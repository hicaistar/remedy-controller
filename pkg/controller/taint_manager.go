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
	client     clientset.Interface
	recorder   record.EventRecorder
	eventCh    chan string
	nodeEvents map[string]string
	nodeConds   map[string]string
}

// NewTaintManager creates a new TaintManager that will use passed clientset to
// communicate with the API server.
func NewTaintManager(c clientset.Interface, rules []Rule) *TaintManager {
	// event recorder
	eventBroadcaster := record.NewBroadcaster()
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "remedy-taint-controller"})
	eventBroadcaster.StartLogging(glog.Infof)
	if c != nil {
		glog.Infof("Sending events to api server")
		eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: c.CoreV1().Events("")})
	} else {
		glog.Fatalf("kubeClient is nil when starting RemedyController")
	}

	// convert rules to types
	nodeEvents, nodeConds := convertRulesToTypes(rules)
	glog.Infof("get rules: %v, %v", nodeEvents, nodeConds)

	tm := &TaintManager{
		client:     c,
		recorder:   recorder,
		nodeConds:  nodeConds,
		nodeEvents: nodeEvents,
	}
	return tm
}

// Run TaintManager
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

// convertRulesToTypes get specified conditions and reasons
func convertRulesToTypes(rules []Rule) (map[string]string, map[string]string) {
	events := make(map[string]string)
	conds := make(map[string]string)
	for _, rule := range rules {
		if string(rule.Type) == Temp {
			events[rule.Reason] = Temp
		}
		if string(rule.Type) == Perm {
			conds[rule.Condition] = rule.Reason
		}
	}
	return events, conds
}