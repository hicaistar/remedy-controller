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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/controller/nodelifecycle/scheduler"
	kubepod "k8s.io/kubernetes/pkg/kubelet/pod"
	"k8s.io/client-go/util/workqueue"
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
	nodeUpdateQueue         workqueue.Interface
}

const (
	retries                 = 5
	loopPeriod              = 1
	graceUncordonNodePeriod = 10
)

// NewDrainManager creates a new DrainManager that will use passed clientset to
// communicate with the API server.
func NewDrainManager(c clientset.Interface, config Config) *DrainManager {
	// convert rules to types
	nodeEvents, nodeConds := convertRulesToTypes(config.Rules)
	glog.Infof("get rules, events: (%v), conditions: (%v)", nodeEvents, nodeConds)
	glog.Infof("get uncordon node period: %v minutes.", config.UnCordonNodePeriod)
	eventCh := make(chan *EventType, 1)
	recorders := make(map[string]record.EventRecorder)

	dm := &DrainManager{
		client:                  c,
		recorders:               recorders,
		nodeConds:               nodeConds,
		nodeEvents:              nodeEvents,
		eventCh:                 eventCh,
		nodeException:           make(map[string]bool),
		occurredEvents:          make(map[string][]EventRecord),
		nodeUpdateQueue:         workqueue.New(),
		graceUncordonNodePeriod: time.Minute * time.Duration(graceUncordonNodePeriod),
	}
	if config.UnCordonNodePeriod > 0 {
		dm.graceUncordonNodePeriod = time.Minute * time.Duration(config.UnCordonNodePeriod)
	}

	dm.drainEvictionQueue = scheduler.CreateWorkerQueue(evictPodHandler(dm.client))

	return dm
}

// Run DrainManager
func (dm *DrainManager) Run(stopCh <-chan struct{}) {
	glog.Infof("Starting DrainManager")
	defer glog.Infof("Shutting down DrainManager")
	go wait.Until(dm.remedyNodesLoop, time.Minute*loopPeriod, wait.NeverStop)

	// Function that is responsible for taking work items out of the workqueue and putting them
	// into channels.
	go func(stopCh <-chan struct{}) {
		for {
			item, shutdown := dm.nodeUpdateQueue.Get()
			if shutdown {
				break
			}
			nodeUpdate := item.(*EventType)
			select {
			case <-stopCh:
				dm.nodeUpdateQueue.Done(item)
			    break
			case dm.eventCh <- nodeUpdate:
				glog.Infof("Get node update event: %v", nodeUpdate)
			}
		}
	}(stopCh)

	// Loop that wait for receiving event from channel.
	for {
		select {
		case event := <-dm.eventCh:
			dm.eventsHandler(event)
		case <-stopCh:
			glog.Infof("DrainManager received stop.")
			break
		}
	}
}

// remedyNodesLoop will un-cordon node if the event had happened for the period specified.
func (dm *DrainManager) remedyNodesLoop() {
	for node, records := range dm.occurredEvents {
		// condition is not good.
		if status, ok := dm.nodeException[node]; ok {
			if status {
				glog.Infof("node is unavailable caused by permanent problem.")
				continue
			}
		}
		// no event records.
		if len(records) == 0 {
			dm.unCordonNode(node)
			continue
		}
		// check all event records on this node.
		available := true
		for _, event := range records {
			duration := time.Now().Sub(event.LastTimestamp)
			if duration.Minutes() < dm.graceUncordonNodePeriod.Minutes() {
				available = false
			}
		}
		if available {
			// node is schedulable, uncordon it.
			dm.unCordonNode(node)
			glog.V(5).Infof("node: %v is available, un-cordon it.", node)
		}
	}
}

// NodeUpdated is used to notify DrainManager about Node conditions' changes.
func (dm *DrainManager) NodeUpdated(newNode *v1.Node) {
	newConditions := newNode.Status.Conditions
	// choose conditions matching rules
	for _, c := range newConditions {
		_, ok := dm.nodeConds[string(c.Type)]
		if ok {
			// If one of Node Conditions is 'True', trigger an eviction
			// Skip 'False' and 'Unknown' status
			if c.Status == "True" {
				event := EventType{
					Name:     string(c.Type),
					Type:     Perm,
					NodeName: newNode.Name,
				}
				// If an eviction has been triggered, skip it.
				status, ok := dm.nodeException[newNode.Name]
				if !ok || (ok && !status) {
					dm.nodeUpdateQueue.Add(&event)
				}
				return
			}
		}
	}
	// Unless all of Node Conditions are 'False', set node schedulable.
	if dm.nodeException[newNode.Name] == true {
		glog.Infof("node: %v condition is normal, set it schedulable.", newNode.Name)
		event := EventType{
			Type:     Perm,
			NodeName: newNode.Name,
			NodeCondition: "False",
		}
		dm.nodeUpdateQueue.Add(&event)
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

		// delete the event recorded from occurred events.
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
		dm.nodeUpdateQueue.Add(&e)

		glog.Infof("Add node: %v event: %v to occurred events, and cordon node.", nodeName, new.Reason)
		return
	}

	// Updated event
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
		dm.nodeUpdateQueue.Add(&e)

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
// and record event on this node.
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
	dm.Eventf(v1.EventTypeNormal, event.NodeName, "npd-problem", fmt.Sprintf("cordon node because %v ", event.Name))
	return nil
}

// unCordonNode uncordon the supplied node. Marks it schedulable for new pods.
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

// getPodsAssignedToNode get all pods running on the supplied node.
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
		return []v1.Pod{}, fmt.Errorf("failed to get Pods assigned to node %v, error: %v", nodeName, err)
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

	// node condition node is 'false'
	if event.NodeCondition == "False" {
		dm.unCordonNode(event.NodeName)
		return nil
	}
	// temporary event, only cordon node.
	if event.Type == Temp {
		err = dm.cordonNode(event)
		if err != nil {
			glog.Errorf("cordon node: %v, error: %v", event.NodeName, err)
		}
		return err
	}
	// permanent condition, cordon node and evict pods.
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
		// set node unschedulable inner.
		dm.nodeException[event.NodeName] = true
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

func (dm *DrainManager) Eventf(eventType, nodeName, reason, messageFmt string, args ...interface{}) {
	recorder, found := dm.recorders[nodeName]
	if !found {
		// TODO: If needed use separate client and QPS limit for event.
		recorder = getEventRecorder(dm.client, nodeName)
		dm.recorders[nodeName] = recorder
	}
	ref := &v1.ObjectReference{
		Kind:      "Node",
		Name:      nodeName,
		UID:       types.UID(nodeName),
		Namespace: "",
	}
	recorder.Eventf(ref, eventType, reason, messageFmt, args...)
}
