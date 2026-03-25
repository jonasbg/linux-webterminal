---
difficulty: Intermediate
duration: 60-90 min
tags: [kubernetes, networking, cilium, gateway-api, networkpolicy, services]
---

# Kubernetes Networking

Learn how Kubernetes networking fits together when Cilium provides the dataplane.

## What you'll learn

- The scheduler picks nodes and the kubelet starts Pods
- One Pod gets one IP, even if it has multiple containers
- Services use labels to select backend Pods
- NetworkPolicy declares allowed traffic; Cilium enforces it with eBPF
- Gateway API routes north-south HTTP traffic to Services
- Pod deletion and scaling change backend Pod IPs while Services stay stable

## The cluster

You're connected to a mock Kubernetes API with realistic networking-focused data:

- **3 Talos nodes** with kubelet details
- **Cilium** running on every node as a DaemonSet
- **Gateway API** resources: `Gateway` and `HTTPRoute`
- **Services** and **NetworkPolicy** in a production namespace
- **Dynamic Pods** that get recreated with new IPs when scaled or deleted

This uses the **real kubectl binary**. The commands map closely to how you would inspect a live cluster.
