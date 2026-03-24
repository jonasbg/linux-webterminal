# Kubernetes Networking with Cilium

This lab uses the real `kubectl` binary against a mock Kubernetes API server with realistic cluster data.

The goal is to understand how traffic flows through Kubernetes when Cilium is the networking layer.

You are learning four separate things:

- Kubernetes schedules workloads
- Pods get IP addresses
- Services provide stable access to changing Pods
- Cilium enforces networking and policy using eBPF on each node

## Core model

Keep this model in mind for the whole lab:

1. The **scheduler** chooses which node should run a Pod
2. The **kubelet** on that node starts and monitors the Pod
3. The Pod gets **one IP address**
4. A **Service** selects backend Pods using labels
5. **Cilium** watches Kubernetes objects and programs eBPF state in the kernel
6. **NetworkPolicy** defines allowed traffic, and Cilium enforces it

## Important facts

- One **Pod** gets one IP address
- If a Pod has multiple containers, they **share the same Pod IP**
- Containers in the same Pod share the same network namespace
- A **Service** gets a stable virtual IP for reaching selected Pods
- A **NetworkPolicy** is not the packet filter itself
- Cilium reads Kubernetes objects and enforces behavior using **eBPF programs** on each node

---

## Part 1: Inspect the cluster

### 1.1 Check versions

```sh
kubectl version
kubectl cluster-info
```

### 1.2 List nodes

```sh
kubectl get nodes
kubectl get nodes -o wide
kubectl describe node worker-1
```

Look for:

- node name
- internal IP
- roles
- kubelet version
- conditions

**Key point:** the kubelet is the node agent. After the scheduler picks a node, the kubelet is responsible for running the Pod there.

### 1.3 Inspect system Pods

```sh
kubectl get pods -n kube-system
kubectl get pods -n kube-system -o wide
kubectl get ds -n kube-system
```

Look for Cilium Pods running on every node.

**Key point:** Cilium is not one central component. It runs on each node so it can program the datapath locally.

---

## Part 2: Pods, scheduling, and IPs

### 2.1 View application Pods

```sh
kubectl get pods -A
kubectl get pods -A -o wide
```

Focus on these columns:

- `NAME`
- `READY`
- `STATUS`
- `IP`
- `NODE`

### 2.2 Inspect one Pod

```sh
kubectl get pod shared-net-demo -n default -o yaml
kubectl get pod web-7c8d9f6d5b-r4f6c -n production -o yaml
```

Look for:

- labels
- node assignment
- Pod IP
- owner references

### 2.3 Understand one Pod = one IP

The `shared-net-demo` Pod has multiple containers, but only one Pod IP.

A Pod gets one IP address.

If that Pod contains multiple containers, they all share:

- the same IP
- the same network namespace
- the same port space

That is why containers in the same Pod can communicate over `localhost`.

---

## Part 3: Scheduling and replacement

### 3.1 Check the Deployment

```sh
kubectl get deploy -n production
kubectl get rs -n production
kubectl get deploy web -n production -o yaml
```

The Deployment declares the desired number of replicas.

If you want to edit a resource in this mock environment, use:

```sh
kedit deployment/web -n production
```

This is a convenience alias for `kubectl edit --validate=false`, because the mock API does not fully implement Kubernetes OpenAPI validation.

### 3.2 Scale it up

```sh
kubectl scale deploy/web -n production --replicas=3
kubectl get pods -n production -o wide
```

Observe:

- more Pods appear
- each Pod gets its own IP
- Pods may be placed on different nodes

### 3.3 Delete one Pod

```sh
kubectl delete pod -n production web-7c8d9f6d5b-r4f6c
kubectl get pods -n production -o wide
```

A replacement Pod should appear.

**Key point:** Pods are disposable. The Deployment wants a certain number of replicas, so Kubernetes replaces missing ones. The new Pod gets a new name and a new IP.

---

## Part 4: Services and label selection

### 4.1 List Services

```sh
kubectl get svc -A
kubectl get svc -n production
kubectl get svc web -n production -o yaml
```

Focus on:

- service type
- cluster IP
- port
- selector

### 4.2 Match the Service to Pods

```sh
kubectl get pods -n production --show-labels
kubectl get svc web -n production -o yaml
```

Compare:

- the Service selector
- the labels on the Pods

**Key point:** a Service finds backend Pods using labels.

### 4.3 Why Services matter

Pods come and go. Their IPs can change.

A Service gives clients a stable virtual address, while the backend Pods can be replaced underneath it.

In a Cilium-based cluster, Service lookups are implemented efficiently in the kernel datapath using eBPF map lookups.

---

## Part 5: NetworkPolicy

### 5.1 List policies

```sh
kubectl get networkpolicy -A
kubectl get networkpolicy default-deny -n production -o yaml
kubectl get networkpolicy allow-gateway-to-web -n production -o yaml
```

Look at:

- `podSelector`
- ingress rules
- ports
- allowed peers

### 5.2 What NetworkPolicy is

A `NetworkPolicy` is a Kubernetes API object.

It does **not** filter packets by itself.

Instead:

- Kubernetes stores the policy
- Cilium watches that policy
- Cilium enforces it using eBPF programs on each node

**Key point:** `NetworkPolicy` is the declared intent. eBPF is part of the enforcement mechanism.

### 5.3 Labels matter here too

Labels are used in two important ways:

- Services use labels to choose backends
- NetworkPolicies use labels to decide which Pods rules apply to

Cilium watches this metadata and converts it into efficient datapath state.

---

## Part 6: Gateway API

### 6.1 View gateway resources

```sh
kubectl get gateways -A
kubectl get httproutes -A
```

### 6.2 Inspect a Gateway

```sh
kubectl get gateway edge -n production -o yaml
```

### 6.3 Inspect an HTTPRoute

```sh
kubectl get httproute web -n production -o yaml
```

Focus on:

- listeners
- hostnames
- path matches
- backend references

**Mental model:**

- `Gateway` = entry point into the cluster
- `HTTPRoute` = routing rules for HTTP traffic
- `Service` = stable backend target
- Pods = actual workloads

---

## Part 7: Cilium and eBPF

### 7.1 Inspect Cilium on the nodes

```sh
kubectl get pods -n kube-system -o wide
kubectl get ds -n kube-system
kubectl logs -n kube-system cilium-worker-1
```

You should see Cilium running across the nodes.

### 7.2 What Cilium does

Cilium watches Kubernetes resources such as:

- Pods
- Services
- NetworkPolicies

It then programs eBPF state in the kernel so the node can handle traffic efficiently.

### 7.3 A practical model

Use this mental model:

- the scheduler decides where a Pod should run
- the kubelet starts the Pod on that node
- the Pod gets an IP
- the Service selects Pods using labels
- Cilium programs eBPF-based forwarding and policy enforcement
- traffic is allowed, denied, or load-balanced in the kernel datapath

### 7.4 What to remember about labels

Cilium does not repeatedly query Kubernetes labels for every packet.

Instead:

- it watches Kubernetes resources
- derives runtime state from labels and selectors
- stores datapath-relevant information in eBPF maps
- uses fast kernel lookups during packet handling

That is why this is more efficient than older approaches based on large iptables rule chains.

---

## Part 8: Suggested review commands

```sh
kubectl get nodes -o wide
kubectl get pods -A -o wide
kubectl get svc -A
kubectl get networkpolicy -A
kubectl get gateways -A
kubectl get httproutes -A
kubectl get ds -n kube-system
kubectl logs -n kube-system cilium-worker-1
```

## Summary

You should now be able to explain:

- what the scheduler does
- what the kubelet does
- why one Pod gets one IP
- why multiple containers in one Pod share the same IP
- how a Service selects Pods using labels
- what a NetworkPolicy is
- why NetworkPolicy is not itself the packet filter
- how Cilium uses eBPF to enforce policy and steer traffic
- how Gateway and HTTPRoute relate to Services and Pods
