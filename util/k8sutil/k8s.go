package k8sutil

import (
	"fmt"
	"os"
	"strings"

	"github.com/ohsu-comp-bio/funnel/config"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// inClusterNamespaceFile is the path to the namespace file that Kubernetes
// mounts into every pod via its ServiceAccount.
const inClusterNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// NewK8sClient returns a new Kubernetes client using in-cluster configuration.
// Funnel's Kubernetes commands (server and cleanup CronJob alike) are designed to
// run inside the cluster, so only in-cluster config is supported.
func NewK8sClient(conf *config.Config) (*kubernetes.Clientset, error) {
	kubeconfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("building in-cluster kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

// InClusterNamespace returns the namespace the current pod's ServiceAccount is
// bound to, read from the file Kubernetes mounts into every pod. It returns an
// empty string when not running inside a cluster.
func InClusterNamespace() string {
	data, err := os.ReadFile(inClusterNamespaceFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
