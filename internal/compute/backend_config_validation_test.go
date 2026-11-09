package compute_test

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/calypr/funnel/internal/compute/aws_batch"
	"github.com/calypr/funnel/internal/compute/gcp_batch"
	"github.com/calypr/funnel/internal/compute/hpc/gridengine"
	"github.com/calypr/funnel/internal/compute/hpc/htcondor"
	"github.com/calypr/funnel/internal/compute/hpc/pbs"
	"github.com/calypr/funnel/internal/compute/hpc/slurm"
	"github.com/calypr/funnel/internal/compute/kubernetes"
	appconfig "github.com/calypr/funnel/internal/config"
)

func TestBackendsRejectInvalidConfigurationBeforeStarting(t *testing.T) {
	tests := []struct {
		name  string
		run   func() error
		field string
	}{
		{"gcp missing project", func() error {
			c := appconfig.DefaultConfig().GCPBatch
			c.Project = ""
			_, err := gcp_batch.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "GCPBatch.Project"},
		{"gcp missing location", func() error {
			c := appconfig.DefaultConfig().GCPBatch
			c.Location = ""
			_, err := gcp_batch.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "GCPBatch.Location"},
		{"slurm malformed template", func() error {
			c := appconfig.DefaultConfig()
			c.Slurm.Template = "{{"
			_, err := slurm.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "Slurm.Template"},
		{"pbs malformed template", func() error {
			c := appconfig.DefaultConfig()
			c.PBS.Template = "{{"
			_, err := pbs.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "PBS.Template"},
		{"htcondor malformed template", func() error {
			c := appconfig.DefaultConfig()
			c.HTCondor.Template = "{{"
			_, err := htcondor.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "HTCondor.Template"},
		{"gridengine malformed template", func() error {
			c := appconfig.DefaultConfig()
			c.GridEngine.Template = "{{"
			_, err := gridengine.NewBackend(c, nil, nil, nil)
			return err
		}, "GridEngine.Template"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("error = %v, want %s", err, tt.field)
			}
		})
	}
}

func TestDisabledReconcilerDoesNotRequireInterval(t *testing.T) {
	conf := appconfig.DefaultConfig()
	conf.Slurm.DisableReconciler = true
	conf.Slurm.ReconcileRate = nil
	_, err := slurm.NewBackend(context.Background(), conf, nil, nil, nil)
	if err != nil {
		t.Fatalf("disabled reconciler rejected missing interval: %v", err)
	}
}

func TestBackendsRejectInvalidReconcileIntervals(t *testing.T) {
	tests := []struct {
		name  string
		run   func() error
		field string
	}{
		{"aws", func() error {
			c := appconfig.DefaultConfig().AWSBatch
			c.DisableReconciler = false
			c.ReconcileRate = durationpb.New(0)
			_, err := aws_batch.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "AWSBatch.ReconcileRate"},
		{"gcp", func() error {
			c := appconfig.DefaultConfig().GCPBatch
			c.ReconcileRate = durationpb.New(0)
			_, err := gcp_batch.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "GCPBatch.ReconcileRate"},
		{"slurm", func() error {
			c := appconfig.DefaultConfig()
			c.Slurm.DisableReconciler = false
			c.Slurm.ReconcileRate = durationpb.New(0)
			_, err := slurm.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "Slurm.ReconcileRate"},
		{"pbs", func() error {
			c := appconfig.DefaultConfig()
			c.PBS.DisableReconciler = false
			c.PBS.ReconcileRate = durationpb.New(0)
			_, err := pbs.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "PBS.ReconcileRate"},
		{"htcondor", func() error {
			c := appconfig.DefaultConfig()
			c.HTCondor.DisableReconciler = false
			c.HTCondor.ReconcileRate = durationpb.New(0)
			_, err := htcondor.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "HTCondor.ReconcileRate"},
		{"kubernetes", func() error {
			c := appconfig.DefaultConfig()
			c.Kubernetes.Namespace = "default"
			c.Kubernetes.WorkerTemplate = "template"
			c.Kubernetes.ReconcileRate = durationpb.New(0)
			_, err := kubernetes.NewBackend(context.Background(), c, nil, nil, nil)
			return err
		}, "Kubernetes.ReconcileRate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("error = %v, want %s", err, tt.field)
			}
		})
	}
}
