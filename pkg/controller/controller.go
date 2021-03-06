package controller

import (
	"encoding/json"
	"io/ioutil"
	"time"

	"github.com/golang/glog"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/controller"
	nodeutil "k8s.io/kubernetes/pkg/controller/util/node"

	"remedy-controller/cmd/options"
)

type Controller struct {
	stopCh              <-chan struct{}
	drainManager        *DrainManager
	nodeLister          corelisters.NodeLister
	nodeInformerSynced  cache.InformerSynced
	eventInformerSynced cache.InformerSynced
	kubeClient          clientset.Interface
}

func NewRemedyController(options *options.RemedyControllerOptions) (*Controller, error) {
	// parse user configurations.
	config := Config{}
	var rules []Rule
	for _, configPath := range options.MonitorConfigPaths {
		rule := getRulesFromConfigFiles(configPath)
		rules = append(rules, rule...)
	}
	if len(rules) == 0 {
		glog.Fatalf("There is no rules found in config files.")
	}
	config.Rules = rules
	config.UnCordonNodePeriod = options.GraceUncordonNodePeriod

	// init kubernetes client
	kubeClient, err := newKubeClient()
	if err != nil {
		return nil, err
	}

	// taint controller
	tc := &Controller{
		kubeClient: kubeClient,
	}

	// node informer and event informer
	// TODO: add some filters here??
	informerFactory := informers.NewSharedInformerFactory(kubeClient, time.Second*60)
	nodeInformer := informerFactory.Core().V1().Nodes()
	eventInformer := informerFactory.Core().V1().Events()
	go informerFactory.Start(tc.stopCh)

	tc.nodeLister = nodeInformer.Lister()
	tc.nodeInformerSynced = nodeInformer.Informer().HasSynced
	tc.eventInformerSynced = eventInformer.Informer().HasSynced

	// new drain manager with rules.
	tc.drainManager = NewDrainManager(kubeClient, config)

	// node condition informer
	nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: nodeutil.CreateUpdateNodeHandler(func(oldNode, newNode *v1.Node) error {
			tc.drainManager.NodeUpdated(newNode)
			return nil
		}),
	})

	// node events informer
	eventInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			event := obj.(*v1.Event)
			tc.drainManager.NodeEventUpdated(nil, event, false)
		},
		UpdateFunc: func(pre, obj interface{}) {
			preEvent := pre.(*v1.Event)
			newEvent := obj.(*v1.Event)
			tc.drainManager.NodeEventUpdated(preEvent, newEvent, false)
		},
		DeleteFunc: func(obj interface{}) {
			event := obj.(*v1.Event)
			tc.drainManager.NodeEventUpdated(nil, event, true)
		},
	})

	return tc, nil
}

func newKubeClient() (clientset.Interface, error) {
	// In-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		glog.Errorf("Get in-cluster config error.")
		return nil, err
	}

	client, err := clientset.NewForConfig(config)
	if err != nil {
		glog.Errorf("Create client error.")
		return nil, err
	}
	return client, nil
}

// Run starts an loop that monitors the status and events of cluster nodes.
func (tc *Controller) Run() {
	glog.Infof("Starting remedy controller")

	if !controller.WaitForCacheSync("remedy", tc.stopCh, tc.nodeInformerSynced, tc.eventInformerSynced) {
		glog.Errorf("wait for cache sync error.")
		return
	}

	// run taintManager forever.
	tc.drainManager.Run(wait.NeverStop)

	<-tc.stopCh
}

// getRulesFromConfigFiles get rules from config file
func getRulesFromConfigFiles(configPath string) []Rule {
	mc := MonitorConfig{}
	f, err := ioutil.ReadFile(configPath)
	if err != nil {
		glog.Fatalf("Failed to read configuration file %q: %v", configPath, err)
	}
	err = json.Unmarshal(f, &mc)
	if err != nil {
		glog.Fatalf("Failed to unmarshal configuration file %q: %v", configPath, err)
	}
	glog.V(5).Infof("Finish parsing monitor config file: %+v", mc)
	return mc.Rules
}
