package controller

import (
	"fmt"
	"github.com/golang/glog"
	"time"

	"k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/controller/nodelifecycle/scheduler"
	kubepod "k8s.io/kubernetes/pkg/kubelet/pod"
	"k8s.io/apimachinery/pkg/types"
)

type DrainManager struct {
	client                  clientset.Interface
	recorders               map[string]record.EventRecorder
	eventCh                 chan *EventType
	nodeEvents              map[string]string
	nodeConds               map[string]string
	nodeException           map[string]bool
	occurredEvents          map[string][]EventRecord
	drainEvictionQueue      *scheduler.TimedWorkerQueue
	graceUncordonNodePeriod time.Duration
}

const (
	retries                 = 5
	graceUncordonNodePeriod = 10
)

// NewTaintManager creates a new TaintManager that will use passed clientset to
// communicate with the API server.
func NewDrainManager(c clientset.Interface, rules []Rule) *DrainManager {
	// convert rules to types
	nodeEvents, nodeConds := convertRulesToTypes(rules)
	glog.Infof("get rules, events: (%v), conditions: (%v)", nodeEvents, nodeConds)
	eventCh := make(chan *EventType, 1)
	recorders := make(map[string]record.EventRecorder)

	dm := &DrainManager{
		client:                  c,
		recorders:                recorders,
		nodeConds:               nodeConds,
		nodeEvents:              nodeEvents,
		eventCh:                 eventCh,
		nodeException:           make(map[string]bool),
		occurredEvents:          make(map[string][]EventRecord),
		graceUncordonNodePeriod: time.Minute * time.Duration(graceUncordonNodePeriod),
	}
	dm.drainEvictionQueue = scheduler.CreateWorkerQueue(evictPodHandler(dm.client))

	return dm
}

// Run TaintManager
func (dm *DrainManager) Run(stopCh <-chan struct{}) {
	glog.Infof("Starting TaintManager")
	defer glog.Infof("Shutting down TaintManager")
	go wait.Until(dm.remedyNodesLoop, time.Minute*5, wait.NeverStop)
	for {
		select {
		case event := <-dm.eventCh:
			dm.eventsHandler(event)
		case <-stopCh:
			glog.Infof("received stop.")
			break
		}
	}
}

// remedyNodesLoop will un-cordon node if the event had happened for the duration specified.
func (dm *DrainManager) remedyNodesLoop() {
	for node, records := range dm.occurredEvents {
		if status, ok := dm.nodeException[node]; ok {
			if status {
				glog.Infof("node is unavailable caused by permanent problem.")
				continue
			}
		}
		if len(records) == 0 {
			dm.unCordonNode(node)
			continue
		}
		for _, event := range records {
			available := true
			duration := time.Now().Sub(event.LastTimestamp)
			if duration.Minutes() < dm.graceUncordonNodePeriod.Minutes() {
				available = false
			}
			if available {
				// un-cordon node
				dm.unCordonNode(node)
				glog.V(5).Infof("node: %v is available, un-cordon it.", node)
			}
		}
	}
}

// NodeUpdated is used to notify TaintManager about Node conditions' changes.
func (dm *DrainManager) NodeUpdated(newNode *v1.Node) {
	newConditions := newNode.Status.Conditions
	// choose conditions matching rules
	for _, c := range newConditions {
		_, ok := dm.nodeConds[string(c.Type)]
		if ok {
			// If one of Node Conditions is 'True', trigger eviction
			// Skip 'False' and 'Unknown' status
			if c.Status == "True" {
				event := EventType{
					Name:     string(c.Type),
					Type:     Perm,
					NodeName: newNode.Name,
				}
				// If eviction has been triggered, skip it.
				status, ok := dm.nodeException[newNode.Name]
				if !ok || (ok && !status) {
					dm.eventCh <- &event
					dm.nodeException[newNode.Name] = true
				}
				return
			}
		}
	}
	// Unless all of Node Conditions are 'False', set node schedulable.
	if dm.nodeException[newNode.Name] == true {
		glog.Infof("node: %v condition is normal, set it schedulable.", newNode.Name)
		dm.unCordonNode(newNode.Name)
	}
	dm.nodeException[newNode.Name] = false
}

// NodeEventsAdded is used to notify TaintManager about Node Events add.
func (dm *DrainManager) NodeEventUpdated(pre, new *v1.Event, isDeleted bool) {
	// skip other events
	if new.InvolvedObject.Kind != "Node" {
		return
	}

	if _, ok := dm.nodeEvents[new.Reason]; !ok {
		// type is not serviced.
		return
	}
	// get node name
	nodeName := new.Source.Host

	if isDeleted {
		preEvents, ok := dm.occurredEvents[nodeName]
		if !ok {
			glog.Errorf("node: %v has no events in occurred events. Occurred events: %v", nodeName, dm.occurredEvents)
			return
		}

		for i, event := range preEvents {
			if event.Name == new.Reason {
				preEvents = append(preEvents[:i], preEvents[i+1:]...)
			}
		}

		dm.occurredEvents[nodeName] = preEvents
		glog.Infof("Delete event: %v from occurred events, now events: %v", new.Reason, dm.occurredEvents)
		return
	}
	// Added event
	if pre == nil {
		// add it to occurredEvents
		newEventRecord := EventRecord{
			Name:          new.Reason,
			LastTimestamp: time.Now(),
		}
		dm.occurredEvents[nodeName] = append(dm.occurredEvents[nodeName], newEventRecord)

		// trigger cordon node.
		e := EventType{
			Name:     new.Reason,
			Type:     Temp,
			NodeName: new.Source.Host,
		}
		dm.eventCh <- &e

		glog.Infof("Add node: %v event: %v to occurred events, and cordon node.", nodeName, new.Reason)
		return
	}

	// Update event
	if pre != nil && pre.Count != new.Count {
		// update occurred events time
		preEvents, ok := dm.occurredEvents[nodeName]
		if !ok {
			glog.Errorf("node: %v has no events in occurred events. Occurred events: %v", nodeName, dm.occurredEvents)
			return
		}
		for i, event := range preEvents {
			if event.Name == new.Reason {
				event.LastTimestamp = time.Now()
			}
			preEvents[i] = event
		}

		dm.occurredEvents[nodeName] = preEvents

		// trigger cordon node.
		e := EventType{
			Name:     new.Reason,
			Type:     Temp,
			NodeName: new.Source.Host,
		}
		dm.eventCh <- &e

		glog.Infof("Update: event %v occur again.", new.Reason)
	}
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

// cordonNode cordon the supplied node. Marks it unschedulable for new pods.
func (dm *DrainManager) cordonNode(event *EventType) error {
	node, err := dm.client.CoreV1().Nodes().Get(event.NodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cordon get node error: %v", err)
	}
	if node.Spec.Unschedulable {
		return nil
	}
	node.Spec.Unschedulable = true
	if _, err := dm.client.CoreV1().Nodes().Update(node); err != nil {
		return fmt.Errorf("cordon update node error: %v", err)
	}
	dm.Eventf(v1.EventTypeNormal, event.NodeName,"npd-problem", fmt.Sprintf("cordon node because %v ", event.Name))
	return nil
}

// cordonNode cordon the supplied node. Marks it unschedulable for new pods.
func (dm *DrainManager) unCordonNode(nodeName string) error {
	node, err := dm.client.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cordon get node error: %v", err)
	}
	if !node.Spec.Unschedulable {
		return nil
	}
	node.Spec.Unschedulable = false
	if _, err := dm.client.CoreV1().Nodes().Update(node); err != nil {
		return fmt.Errorf("cordon update node error: %v", err)
	}
	return nil
}

func (dm *DrainManager) getPodsAssignedToNode(nodeName string) ([]v1.Pod, error) {
	selector := fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName})
	pods, err := dm.client.CoreV1().Pods(v1.NamespaceAll).List(metav1.ListOptions{
		FieldSelector: selector.String(),
		LabelSelector: labels.Everything().String(),
	})
	for i := 0; i < retries && err != nil; i++ {
		pods, err = dm.client.CoreV1().Pods(v1.NamespaceAll).List(metav1.ListOptions{
			FieldSelector: selector.String(),
			LabelSelector: labels.Everything().String(),
		})
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return []v1.Pod{}, fmt.Errorf("failed to get Pods assigned to node %v", nodeName)
	}
	return pods.Items, nil
}

// evictPodHandler evict the supplied pod.
func evictPodHandler(c clientset.Interface) func(args *scheduler.WorkArgs) error {
	return func(args *scheduler.WorkArgs) error {
		ns := args.NamespacedName.Namespace
		name := args.NamespacedName.Name
		glog.Infof("DrainManager is evicting Pod: %v", args.NamespacedName.String())
		var err error
		for i := 0; i < retries; i++ {
			err = c.CoreV1().Pods(ns).Evict(&policy.Eviction{
				ObjectMeta:    metav1.ObjectMeta{Namespace: ns, Name: name},
				DeleteOptions: nil,
			})
		}
		if err != nil {
			glog.Errorf("evict Pod: %v error: %v.", args.NamespacedName.String(), err)
		}
		return err
	}
}

// eventsHandler cordon node or drain node according the event type.
func (dm *DrainManager) eventsHandler(event *EventType) error {
	glog.Infof("eventsHandler receive event: %v", event)
	var err error
	if event == nil {
		return nil
	}
	if event.Type == Temp {
		err = dm.cordonNode(event)
		if err != nil {
			glog.Errorf("cordon node: %v, error: %v", event.NodeName, err)
		}
		return err
	}
	if event.Type == Perm {
		err = dm.cordonNode(event)
		if err != nil {
			glog.Errorf("cordon node: %v, error: %v", event.NodeName, err)
			return err
		}
		// get pods running on node
		pods, err := dm.getPodsAssignedToNode(event.NodeName)
		if err != nil {
			glog.Errorf(err.Error())
			return err
		}
		if len(pods) == 0 {
			return nil
		}

		now := time.Now()
		for i := range pods {
			pod := &pods[i]
			if isPodNoNeedToEvict(pod) {
				continue
			}
			dm.drainEvictionQueue.AddWork(scheduler.NewWorkArgs(pod.Name, pod.Namespace), now, now)
		}
	}
	return nil
}

// isPodNoNeedToEvict filter static pods and DaemonSet-owned pods
func isPodNoNeedToEvict(pod *v1.Pod) bool {
	isDaemonSetPod := false
	// is DaemonSet owned pod
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			isDaemonSetPod = true
			glog.Infof("pod: %v is no need to evict from DaemonSet", pod.Name)
		}
	}
	return kubepod.IsStaticPod(pod) || isDaemonSetPod
}

// getEventRecorder generates a recorder for specific node name.
func getEventRecorder(c clientset.Interface, nodeName string) record.EventRecorder {
	// event recorder
	eventBroadcaster := record.NewBroadcaster()
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "remedy-controller", Host: nodeName})
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: c.CoreV1().Events("")})
	glog.V(5).Infof("Create event recorder for node: %s", nodeName)
	return recorder
}

func(dm *DrainManager) Eventf(eventType, nodeName, reason, messageFmt string, args ...interface{}) {
	recorder, found := dm.recorders[nodeName]
	if !found {
		// TODO: If needed use separate client and QPS limit for event.
		recorder = getEventRecorder(dm.client, nodeName)
		dm.recorders[nodeName] = recorder
	}
	ref := &v1.ObjectReference{
		Kind: "Node",
		Name: nodeName,
		UID: types.UID(nodeName),
		Namespace: "",
	}
	recorder.Eventf(ref, eventType, reason, messageFmt, args...)
}