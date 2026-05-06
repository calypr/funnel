# K8s Resources

This directory contains the following resources that are created for each task submitted to a Funnel server running in a Kubernetes cluster.

The order of resource creation is:
1. PersistentVolume
2. PersistentVolumeClaim
3. ConfigMap
4. Job

## PersistentVolume

The backend storage containing any task inputs and outputs

> ref: https://kubernetes.io/docs/concepts/storage/persistent-volumes/

## PersistentVolumeClaim

The "request" for storage that connects the PersistentVolume and the Job

## ConfigMap

The Worker Config (`funnel-worker-{Task ID}.yaml`)

## Job

The Worker and Executor Jobs themselves
