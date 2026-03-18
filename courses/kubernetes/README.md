---
difficulty: Intermediate
duration: 60-90 min
tags: [kubernetes, kubectl, pods, deployments, argocd, talos]
---

# Kubernetes Basics

Learn to navigate a Kubernetes cluster using the real `kubectl`.

## What you'll learn

- Kubernetes is just an API server with a database - every resource is a REST object
- Use `kubectl` to explore namespaces, pods, deployments, services, and more
- Read logs, edit deployments, create resources from YAML
- Understand how ConfigMaps and Secrets store configuration
- Discover that Secrets are base64-encoded, not encrypted
- Inspect Talos Linux nodes and ArgoCD applications
- See that every `kubectl` command is just an HTTP request

## The cluster

You're connected to a mock Kubernetes API with realistic data:

- **3 Talos nodes** (1 control-plane, 2 workers)
- **Namespaces**: default, kube-system, production, argocd
- **Workloads**: nginx deployment, PostgreSQL statefulset, API server
- **Config**: ConfigMaps, Secrets (try decoding them!)
- **GitOps**: ArgoCD applications with sync/health status

This uses the **real kubectl binary** - the same commands work on any production cluster.

## Who is this for?

Anyone starting with Kubernetes. You should understand what containers are (take the Container Fundamentals course first if not). No cluster setup needed - just start typing `kubectl`.
