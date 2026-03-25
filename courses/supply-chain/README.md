---
difficulty: Intermediate
duration: 90-120 min
tags: [supply-chain, trivy, sbom, digests, ci-cd, security]
---

# Supply Chain

Learn how to trust, verify, and gate what gets shipped.

## What you'll learn

- why tags are mutable and digests are safer
- why `image:tag@sha256:digest` is often the best practical reference form
- how to scan images with Trivy
- how to generate and inspect an SBOM
- how CI/CD gates turn findings into delivery policy
- where provenance, signing, and trust fit in the software supply chain

## The workshop

You get two example Dockerfiles, a pinned Trivy installation, and a simple policy script.

Build the insecure and hardened images, compare scan output, generate an SBOM, and then apply a basic gate the same way a CI pipeline would.

## Who is this for?

Developers and platform engineers who already know basic container workflows and want to understand what should block a release, what should be measured, and what should be trusted.
