package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
)

var fun *tests.Funnel
var clusterName string

// checkDependencies checks if the required dependencies (k3d and helm) are installed.
// K3d: https://k3d.io/
// Helm: https://helm.sh/
func checkDependencies() error {
	cmd := exec.Command("k3d", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("k3d command not found: %v", err)
	}

	cmd = exec.Command("helm", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm command not found: %v", err)
	}

	return nil
}

// TestMain sets up the test environment for Kubernetes integration tests.
func TestMain(m *testing.M) {
	if err := checkDependencies(); err != nil {
		logger.Debug("Test dependencies not found:", err)
		os.Exit(0)
	}

	tests.ParseConfig()
	conf := tests.DefaultConfig()

	// Set the compute type to Kubernetes
	conf.Compute = "kubernetes"

	// Set the cluster name
	clusterName = "funnel-test-cluster-" + tests.RandomString(6)

	// TODO: This pattern is used when running tests from the Makefile (e.g. `make test-slurm`)
	// We don't have any `test-K8s` target, but can add one if needed.
	if conf.Compute != "kubernetes" {
		logger.Debug("Skipping kubernetes e2e tests...")
		os.Exit(0)
	}

	// Create the K8s cluster
	err := StartK8sCluster(clusterName)
	if err != nil {
		logger.Debug("failed to start K8s cluster:", err)
		os.Exit(1)
	}

	kubeconfig := filepath.Join(os.TempDir(),
		"funnel",
		fmt.Sprintf("%s-kubeconfig.yaml", clusterName),
	)

	// Write the kubeconfig to a temporary file
	err = WriteKubeconfig(clusterName, kubeconfig)
	if err != nil {
		logger.Debug("failed to get kubeconfig:", err)
		os.Exit(1)
	}

	// Start the Funnel server in the K8s cluster using the Helm charts in the K8s deployments directory
	err = StartServerInK8s(clusterName, "../../deployments/kubernetes/helm/")
	if err != nil {
		logger.Debug("failed to start funnel server in K8s:", err)
		os.Exit(1)
	}

	// Create a Funnel instance with a K8s client
	conf.Server.RPCPort = "9090"
	conf.Server.HTTPPort = "8080"
	fun = tests.NewFunnel(conf)
	fun.StartServer()

	exit := 0
	defer func() {
		// Cleanup the test K8s cluster
		err = DeleteK8sCluster(clusterName, kubeconfig)
		if err != nil {
			logger.Debug("failed to delete K8s cluster:", err)
		}
		os.Exit(exit)
	}()

	exit = m.Run()
	return
}

// StartK8sCluster creates a K8s cluster for integration tests.
func StartK8sCluster(clusterName string) error {
	cmd := exec.Command("k3d", "cluster", "create", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create K8s cluster: %v", err)
	}

	logger.Debug("K8s cluster created successfully", "Cluster Name", clusterName)

	return nil
}

// WriteKubeconfig retrieves the kubeconfig for the K8s cluster.
// Example: `/tmp/funnel/funnel-test-cluster-abc123-kubeconfig.yaml`
func WriteKubeconfig(clusterName string, kubeconfig string) error {
	cmd := exec.Command("k3d", "kubeconfig", "get", clusterName)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig for K8s cluster: %v", err)
	}

	err = os.MkdirAll(filepath.Dir(kubeconfig), os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create directory for kubeconfig: %v", err)
	}

	err = os.WriteFile(kubeconfig, output, 0644)
	if err != nil {
		return fmt.Errorf("failed to write kubeconfig to file: %v", err)
	}

	return nil
}

// DeleteK8sCluster tears down the test K8s cluster.
func DeleteK8sCluster(clusterName string, kubeconfig string) error {
	cmd := exec.Command("k3d", "delete", "cluster", clusterName, "--config", kubeconfig)

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to delete K8s cluster: %v", err)
	}

	return nil
}

// StartServerInK8s deploys the Funnel server in the K8s cluster using Helm.
func StartServerInK8s(clusterName string, chartPath string) error {
	cmd := exec.Command("helm", "upgrade", "--install", "funnel", chartPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to deploy Funnel with Helm: %v", err)
	}

	// Wait until the Funnel server is ready
	cmd = exec.CommandContext(context.Background(), "kubectl", "rollout", "status", "deployment/funnel-server", "--timeout", "180s")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to wait for Funnel server to be ready: %v", err)
	}

	logger.Debug("Funnel server deployed successfully in K8s cluster", "Cluster Name", clusterName)

	return nil
}

func PortForwardFunnel(t *testing.T, namespace, svcName string, ports ...string) *exec.Cmd {
	args := []string{"port-forward", "svc/funnel"}
	args = append(args, ports...)

	cmd := exec.Command("kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start port-forward: %v", err)
	}

	return cmd
}

// TestHelloWorld runs a simple hello world task in the Kubernetes cluster.
func TestHelloWorld(t *testing.T) {
	id, err := fun.RunE(`
    --sh 'echo hello world'
    `)

	if err != nil {
		t.Fatal("failed to run task:", err)
	}

	task := fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("expected task to be in complete state; got:", task.State.String())
	}

	if task.Logs[0].Logs[0].Stdout != "hello world\n" {
		t.Fatal("Missing stdout")
	}
}
