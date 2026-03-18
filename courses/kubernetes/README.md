# Kubernetes Basics

Learn to navigate a Kubernetes cluster using the real `kubectl` binary against a mock API server.

## What's included

- **instruction.md** - Guided walkthrough of kubectl, resources, namespaces, and the API model
- **kubectl** - Real kubectl binary (v1.31.0)
- **kube-mock** - Lightweight mock Kubernetes API server with pre-populated cluster data
- **kubeconfig** - Pre-configured to point at the mock API

## Mock cluster contents

- `kube-system`: coredns pod
- `default`: nginx deployment (2 pods), nginx service, app-config configmap
- `production`: api-server deployment (3 pods), postgres statefulset, services, configmaps, secrets

## What students learn

- kubectl get, describe, edit, delete, apply, logs
- Pods, Deployments, StatefulSets, Services, ConfigMaps, Secrets
- Namespaces as organizational units
- The API model: everything is a resource, kubectl is just an HTTP client
- Secrets are base64, not encrypted

## Security profile: strict

The mock API server runs on localhost:8080 inside the container. No external network needed.

## Building

```bash
podman build -t ghcr.io/jonasbg/linux-webterminal/terminal-kubernetes:latest .
```
