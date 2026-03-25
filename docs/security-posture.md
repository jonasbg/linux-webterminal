# Security Posture

This document describes the security posture of the `linux-webterminal` deployment as it exists today, the risks that are intentionally accepted, and the controls that matter most for this specific environment.

## Scope

This assessment is for the current deployment model:

- a dedicated VM used only for this application
- no access from that VM into private/internal infrastructure
- SSH access restricted to public keys
- Cloudflare in front of the service
- geographic restrictions applied at the edge
- the host is considered recoverable and replaceable

This is **not** the posture for a shared production host or a host with access to internal systems.

## Threat Model

The realistic threats for this VM are:

- abuse of compute, disk, or network resources
- arbitrary container creation if the web app is compromised
- persistence on this VM
- tampering with the application for later users
- public-IP abuse originating from the VM

The main remaining architectural risk is that `terminal-server` has access to the host user's rootless Podman socket. A compromise of the server container can therefore become a compromise of the `containeruser` Podman control plane on this VM.

Because this VM is isolated from the rest of the environment, the expected impact is primarily limited to this VM itself rather than lateral movement into other infrastructure.

## What Is Accepted

The following risks are currently accepted:

- `terminal-server` can control the host rootless Podman runtime for `containeruser`
- Docker and Supply Chain course sessions require a permissive builder profile with nested Podman support
- a compromise of the web app could result in arbitrary containers being created on this VM as `containeruser`
- the VM should be treated as a potentially disposable sandbox host, not a trusted control-plane machine

These risks are accepted because:

- the VM is dedicated to this application
- there are no trusted internal services reachable from it
- the host can be recovered or rebuilt quickly

## Controls Already In Place

### Isolation and Exposure

- Dedicated VM for this application only
- No trusted internal infrastructure reachable from the VM
- Public access constrained by Cloudflare and geo restrictions

### Account Hygiene

- `containeruser` is no longer in `wheel`
- `containeruser` is set to `/sbin/nologin`
- administrative access is performed as `jonasbg`
- non-runtime content has been moved away from `containeruser`

### Runtime and Session Controls

- session containers are labeled and auto-cleaned by the application
- session lifetime is capped with `CONTAINER_LIFETIME`
- concurrent sessions are capped with `MAX_CONTAINERS`
- strict courses use a locked-down runtime profile
- builder courses use a narrower profile than before:
  - `userns_mode=host` removed
  - explicit memory, CPU, and PID limits

### Deployment Hygiene

- course configuration is externalized to YAML
- image refs can be pinned by digest
- CI publishes pushed image digests

## Current Residual Risks

These are the main residual risks that still matter:

1. `terminal-server` has the host Podman socket mounted
   - this is the primary remaining control-plane risk

2. Builder courses still require privileged nested container behavior
   - Docker Workshop and Supply Chain are materially less confined than the other courses

3. Abuse of this VM remains possible
   - CPU mining
   - arbitrary image pulls
   - container spam
   - disk exhaustion
   - network abuse from this VM

4. Persistence on this VM remains possible if the application is compromised
   - limited mostly to this VM and the `containeruser` runtime scope

## What "Good Enough" Means Here

For this VM, "good enough" does **not** mean perfect containment. It means:

- compromise is expected to stay contained to this VM
- no trusted credentials or internal access are present
- the VM can be recovered quickly
- resource abuse is bounded enough to be operationally tolerable
- suspicious activity can be noticed and cleaned up

If those conditions remain true, the current design can be an acceptable risk tradeoff for a public lab platform.

## Operator Priorities

The highest-value ongoing controls are:

1. Fast recovery
   - keep snapshots or rebuild capability
   - prefer pull-only deploys from CI-built images

2. Resource governance
   - keep `MAX_CONTAINERS` aligned with actual capacity
   - keep `CONTAINER_LIFETIME` bounded
   - keep CPU, memory, and PID caps on builder sessions

3. Monitoring
   - inspect `podman ps -a`
   - inspect `podman system df`
   - inspect application logs
   - monitor disk growth and unusual container creation

4. Minimize what is valuable on the VM
   - no sensitive infrastructure credentials
   - no internal network trust
   - no unnecessary admin material in runtime accounts

5. Keep image pinning and config management simple
   - use digest-pinned images where practical
   - keep course/runtime config outside application code

## Triggers For Reassessment

This posture should be reconsidered if any of the following changes:

- the VM gains access to internal infrastructure
- secrets or sensitive credentials are added to the host
- the application is moved to a shared host
- more privileged host mounts are added
- the service begins handling data that must remain confidential

If any of those become true, the Podman socket design should no longer be considered acceptable.

## Recommended Next Steps

These are useful, but not urgent, in the current threat model:

1. Move deployment fully to CI-built, pull-only images
2. Mount pinned course config from a neutral read-only path such as `/opt/linux-webterminal`
3. Keep course image refs pinned by digest
4. Periodically prune unused Podman images and stopped containers for `containeruser`
5. Consider separating builder-heavy courses if they become noisy or abused

## Summary

This deployment is **not** strongly sandboxed in the abstract. However, given that the VM is dedicated, isolated, and disposable, the practical risk is mostly limited to the VM itself.

That makes the current setup a reasonable managed-risk tradeoff for a public training sandbox, provided the host remains isolated from trusted infrastructure and is operated with fast recovery and basic monitoring in mind.
