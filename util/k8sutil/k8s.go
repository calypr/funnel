package k8sutil

import (
	"fmt"

	"github.com/ohsu-comp-bio/funnel/config"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// NewK8sClient returns a new Kubernetes client.
func NewK8sClient(conf *config.Config) (*kubernetes.Clientset, error) {
	var kubeconfig *rest.Config
	var err error

	kubeconfig, err = rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("building in-cluster kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}
