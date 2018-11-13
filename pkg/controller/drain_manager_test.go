package controller

import (
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	"testing"
)

const (
	testPermCondition = "TestPermCondition"
	testTempCondition = "TestTempCondition"
)

func TestIsPodNoNeedToEvict(t *testing.T) {
	pod := v1.Pod{}
	pod.OwnerReferences = append(pod.OwnerReferences, metav1.OwnerReference{
		Kind: "DaemonSet",
	})
	got := isPodNoNeedToEvict(&pod)
	if got != true {
		t.Errorf("case expected status %v, got %v", true, got)
	}
}

func TestConvertRulesToTypes(t *testing.T) {
	rules := []Rule{
		{
			Type:      Perm,
			Condition: testPermCondition,
			Reason:    "node is in permanent problem",
		},
		{
			Type:   Temp,
			Reason: testTempCondition,
		},
	}
	count := 1
	events, conds := convertRulesToTypes(rules)
	_, expectedA := events[testTempCondition]
	if !expectedA {
		t.Errorf("case %d expected status: %v, got %v", count, true, expectedA)
	}
	_, expectedB := conds[testPermCondition]
	if !expectedB {
		t.Errorf("case %d expected status: %v, got %v", count+1, true, expectedB)
	}
}

func TestDrainManager(t *testing.T) {
	config := Config{}
	rules := []Rule{
		{
			Type:      Perm,
			Condition: testPermCondition,
			Reason:    "node is in permanent problem",
		},
		{
			Type:   Temp,
			Reason: testTempCondition,
		},
	}
	config.Rules = rules

	count := 1
	client := fakeclient.NewSimpleClientset()
	dm := NewDrainManager(client, config)
	defer close(dm.eventCh)

	node := v1.Node{}
	node.Name = "test-node"
	node.Status.Conditions = append(node.Status.Conditions, v1.NodeCondition{
		Type:   testPermCondition,
		Status: "True",
	})

	dm.NodeUpdated(&node)
	event := <-dm.eventCh
	if event.NodeName != node.Name {
		t.Errorf("case %d expected status: %v, got %v", count, node.Name, event.NodeName)
	}

	nodeEvent := v1.Event{
		Count: 1,
		Source: v1.EventSource{
			Component: "kubelet",
			Host:      "test-node",
		},
		InvolvedObject: v1.ObjectReference{
			Kind: "Node",
		},
		Reason: testTempCondition,
	}
	dm.NodeEventUpdated(nil, &nodeEvent, false)
	event = <-dm.eventCh
	if event.NodeName != node.Name {
		t.Errorf("case %d expected status: %v, got %v", count+1, node.Name, event.NodeName)
	}

	newEvent := v1.Event{
		Count: 2,
		Source: v1.EventSource{
			Component: "kubelet",
			Host:      "test-node",
		},
		InvolvedObject: v1.ObjectReference{
			Kind: "Node",
		},
		Reason: testTempCondition,
	}
	dm.NodeEventUpdated(&nodeEvent, &newEvent, false)
	dm.remedyNodesLoop()
}
