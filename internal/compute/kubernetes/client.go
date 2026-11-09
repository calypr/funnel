package kubernetes

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// NewClient returns a new Kubernetes client.
func NewClient() (*kubernetes.Clientset, error) {
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
