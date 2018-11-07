package controller

import (
	"time"
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/informers"
	"k8s.io/kubernetes/pkg/controller"
	"k8s.io/apimachinery/pkg/util/wait"
	nodeutil "k8s.io/kubernetes/pkg/controller/util/node"
)


type Controller struct {
	stopCh              <-chan struct{}
	taintManager        *TaintManager
	nodeLister          corelisters.NodeLister
	nodeInformerSynced  cache.InformerSynced
	eventInformerSynced cache.InformerSynced
	kubeClient          clientset.Interface
}

func NewRemedyController() (*Controller, error) {
	kubeClient, err := newKubeClient()
	if err != nil {
		return nil, err
	}

	// taint controller
	tc := &Controller{
		kubeClient: kubeClient,
	}

	// node informer
	informerFactory := informers.NewSharedInformerFactory(kubeClient, time.Second * 20)
	nodeInformer := informerFactory.Core().V1().Nodes()
	eventInformer := informerFactory.Core().V1().Events()
	go informerFactory.Start(tc.stopCh)

	tc.nodeLister = nodeInformer.Lister()
	tc.nodeInformerSynced = nodeInformer.Informer().HasSynced
	tc.eventInformerSynced = eventInformer.Informer().HasSynced

	// taint manager
	tc.taintManager = NewTaintManager(kubeClient)

	// node condition informer
	nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: nodeutil.CreateUpdateNodeHandler(func(oldNode, newNode *v1.Node) error {
			tc.taintManager.NodeUpdated(oldNode, newNode)
			return nil
		}),
	})

	// node events informer
	eventInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			event := obj.(*v1.Event)
			tc.taintManager.NodeEventAdded(event)
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

	tc.taintManager.Run(wait.NeverStop)

	<-tc.stopCh
}
