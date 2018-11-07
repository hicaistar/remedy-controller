package controller

import (
	"k8s.io/node-problem-detector/pkg/types"
	watchertypes "k8s.io/node-problem-detector/pkg/systemlogmonitor/logwatchers/types"
)

// MonitorConfig is the configuration of log monitor.
type MonitorConfig struct {
	// WatcherConfig is the configuration of log watcher.
	watchertypes.WatcherConfig
	// BufferSize is the size (in lines) of the log buffer.
	BufferSize int `json:"bufferSize"`
	// Source is the source name of the log monitor
	Source string `json:"source"`
	// DefaultConditions are the default states of all the conditions log monitor should handle.
	DefaultConditions []types.Condition `json:"conditions"`
	// Rules are the rules log monitor will follow to parse the log file.
	Rules []Rule `json:"rules"`
}

// Rule describes how log monitor should analyze the log.
type Rule struct {
	// Type is the type of matched problem.
	Type types.Type `json:"type"`
	// Condition is the type of the condition the problem triggered. Notice that
	// the Condition field should be set only when the problem is permanent, or
	// else the field will be ignored.
	Condition string `json:"condition"`
	// Reason is the short reason of the problem.
	Reason string `json:"reason"`
	// Pattern is the regular expression to match the problem in log.
	// Notice that the pattern must match to the end of the line.
	Pattern string `json:"pattern"`
}

const (
	// Temp means the problem is temporary, only need to report an event.
	Temp  = "temporary"
	// Perm means the problem is permanent, need to change the node condition.
	Perm  = "permanent"
)