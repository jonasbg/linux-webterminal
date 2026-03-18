# Container Runtime Fundamentals

How containers actually work under the hood. No magic - just Linux.

## What is a Container?

A container is **not** a virtual machine. It's a regular Linux process with two things applied to it:

1. **Namespaces** - limit what the process can *see*
2. **Cgroups** - limit what the process can *use*

That's it. Everything else (images, registries, orchestrators) is tooling built on top.

## The Runtime Stack

When you run a container, this is what happens:

```
You (podman run / docker run)
  → container runtime (containerd / CRI-O / Podman)
    → OCI runtime (runc / crun)
      → your process
```

Each layer has one job:

| Component | Job |
|-----------|-----|
| **podman / docker** | CLI tool - tells the runtime what to do |
| **containerd / CRI-O** | Pulls images, manages container lifecycle, creates filesystem snapshots |
| **runc / crun** | Actually creates the container (calls Linux `clone()` with namespace flags, sets up cgroups) |

The key insight: **runc is the only part that talks to the kernel**. Everything above it is management.

---

## Part 1: Namespaces - What Can You See?

Namespaces control what a process can see. You're inside a container right now. Let's explore what's been hidden from you.

### 1.1 PID Namespace

Your container has its own process ID space:

```
echo "My shell PID: $$"
ps
```

Notice PID 1 exists - that's your container's init process. On the real host, your shell has a completely different PID. Other containers running on the same machine? Invisible to you.

**Try it:**
```
ls /proc
```

You only see your own processes. On the host, there would be hundreds.

### 1.2 UTS Namespace (Hostname)

```
hostname
```

This hostname is isolated to your container. The host machine has a different one. This is the UTS namespace at work.

### 1.3 Mount Namespace

```
ls /
```

This filesystem is not the host's real filesystem. It's an Alpine Linux image that was unpacked just for your container.

**Try it:**
```
cat /etc/os-release
```

The host might be running Fedora, Ubuntu, or anything else. You see Alpine because that's what the container image contains.

### 1.4 Network Namespace

Your container has its own network stack. In this environment, networking is completely disabled:

**Try it:**
```
cat /etc/resolv.conf 2>/dev/null
```

No DNS. No network interfaces (except loopback). This is the network namespace providing isolation.

### 1.5 See Your Namespaces

Every namespace is represented as a file:

```
ls -la /proc/$$/ns/
```

Each file is a namespace. The kernel assigns your process to these namespaces when the container starts. Two processes with the same namespace file are in the same namespace - they can see each other.

**Question:** What namespaces do you see listed? What do you think each one isolates?

---

## Part 2: Cgroups - What Can You Use?

Cgroups (Control Groups) limit how much of the host's resources your container can consume. You can't see the config files, but you can **feel** the limits.

### 2.1 PID Limit

Your container is limited to 10 processes. Let's prove it:

```
sleep 999 &
sleep 999 &
sleep 999 &
sleep 999 &
sleep 999 &
sleep 999 &
sleep 999 &
```

**Now check:**
```
ps
```

Count the processes. You have your shell, the sleep processes, and ps itself. Now try to add more:

```
sleep 999 &
sleep 999 &
sleep 999 &
```

**What happened?** At some point, the kernel refused to create new processes. That's the `pids.max` cgroup limit in action.

Clean up:
```
kill $(ps | grep sleep | grep -v grep | cut -c1-6) 2>/dev/null
```

### 2.2 Memory Limit

Your container has 64MB of memory. Let's see what happens when we try to use too much:

```
head -c 50000000 /dev/urandom > /tmp/bigfile
```

That's ~50MB. It should work. Now try:

```
head -c 70000000 /dev/urandom > /tmp/bigfile2
```

**What happened?** The cgroup memory limit prevents you from using more than 64MB total. The kernel's OOM (Out Of Memory) killer may terminate your process.

Clean up:
```
rm /tmp/bigfile /tmp/bigfile2 2>/dev/null
```

### 2.3 CPU Limit

Your container is limited to 10% CPU. Let's feel it:

```
i=0; while [ $i -lt 1000000 ]; do i=$((i+1)); done; echo "Done: $i"
```

This loop runs, but it's throttled. On a fast machine without limits, this would complete almost instantly. Your container is sharing CPU fairly with others.

### 2.4 Read-Only Filesystem

The container's root filesystem is read-only:

```
touch /etc/test 2>&1
touch /bin/test 2>&1
```

Both fail. Only `/tmp` and `/home` are writable (they're tmpfs mounts). This prevents a compromised container from modifying its own binaries.

```
touch /tmp/this-works
echo "writable" > /tmp/this-works
cat /tmp/this-works
rm /tmp/this-works
```

### 2.5 Dropped Capabilities

Linux capabilities are fine-grained permissions. Your container has ALL capabilities dropped:

```
id
```

Even though you're a regular user, capabilities would normally allow certain privileged operations. With all capabilities dropped, you can't:
- Change file ownership to other users
- Bind to privileged ports
- Load kernel modules
- Modify the system clock

This is defense in depth - even if an attacker gets code execution, they can't escalate privileges.

---

## Part 3: What is a Container Image?

A container image is simpler than you think:

1. **A manifest** (JSON) - metadata about the image
2. **Layers** (tar.gz files) - each layer is a set of filesystem changes
3. **A config** (JSON) - default command, environment variables, user

When a container runtime "pulls" an image, it:
1. Downloads the manifest
2. Downloads each layer
3. Unpacks and stacks the layers using a snapshot driver (overlayfs)
4. Starts runc/crun with the merged filesystem as the root

**The filesystem you're looking at right now** is the result of this process. Alpine Linux base layer + the course setup layer, merged together.

```
cat /etc/alpine-release
```

That file came from the Alpine base layer.

---

## Part 4: How runc Creates a Container

When runc starts your container, it does roughly this:

1. `clone(CLONE_NEWPID | CLONE_NEWNET | CLONE_NEWNS | CLONE_NEWUTS)` - create new namespaces
2. `pivot_root()` - switch to the container's filesystem
3. Set up cgroup limits (memory, CPU, PIDs)
4. Drop capabilities
5. `exec()` your shell

That's the entire container runtime in 5 system calls. Everything else is management.

---

## Part 5: containerd vs CRI-O vs Podman

All three manage containers, but they have different origins and goals:

| | containerd | CRI-O | Podman |
|--|-----------|-------|--------|
| **Origin** | Docker (extracted from dockerd) | Red Hat (built for CRI) | Red Hat (Docker replacement) |
| **Scope** | General-purpose daemon | CRI daemon | Daemonless CLI |
| **OCI runtime** | runc (default) | runc (default) | crun (default) |
| **Image format** | OCI, Docker | OCI, Docker | OCI, Docker |
| **Rootless** | With setup | With setup | Native |
| **Used by** | Docker Desktop, k3s, EKS, GKE | OpenShift, CRC | Fedora, RHEL, this platform |

They all do the same fundamental thing: unpack images, set up namespaces and cgroups via an OCI runtime, and manage container lifecycle.

---

## Challenges

1. How many processes can you start before hitting the PID limit? Count exactly.
2. What's the largest file you can create in `/tmp` before running out of memory?
3. Run `ls /proc/1/ns/` and `ls /proc/$$/ns/` - are the namespace IDs the same? Why?
4. Try to create a file in `/usr/local/bin/`. What happens and why?
5. If runc just calls `clone()` with flags, what makes containerd/Podman necessary? Why not call runc directly?

## Key Takeaways

- A container is a process with namespaces (isolation) and cgroups (resource limits)
- The runtime stack is: CLI → container runtime → OCI runtime (runc/crun) → your process
- Container images are just tarballs stacked with overlayfs
- runc does the real work: clone, pivot_root, cgroups, exec
- Everything above runc is lifecycle management and convenience
