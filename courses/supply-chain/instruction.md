# Supply Chain

This course focuses on software delivery trust, not just image size.

You will build two container images, compare their scan results, inspect a generated SBOM, and apply a basic release gate.

Files in your home directory:

- `insecure.Dockerfile`
- `hardened.Dockerfile`
- `policy.sh`

## Part 1: Tags, digests, and trust

Mutable tags are convenient but weak:

- `python:3.12-alpine` can change over time
- `python@sha256:...` points to one exact image
- `python:3.12-alpine@sha256:...` gives you both the human-friendly tag and the machine-pinned digest

For teaching and for real CI/CD systems, this is usually the best format:

```text
image:tag@sha256:digest
```

Why:

- the **tag** helps humans understand what they intended to use
- the **digest** is what actually pins the artifact

If the tag later moves in the registry, the digest still pins the original image.

That means:

- `image:tag` is mutable
- `image@sha256:digest` is immutable but less readable
- `image:tag@sha256:digest` gives you both readability and reproducibility

In CI/CD, tags are often used for discovery while digests are used for promotion.

Think in layers:

1. source code trust
2. build system trust
3. base image trust
4. artifact trust
5. deployment policy

---

## Part 2: Build the example images

Build both images:

```sh
docker build -f insecure.Dockerfile -t localhost/supply-chain-insecure .
docker build -f hardened.Dockerfile -t localhost/supply-chain-hardened .
```

Check what you built:

```sh
docker images | grep supply-chain
```

Inspect digests:

```sh
docker image inspect localhost/supply-chain-insecure | jq '.[0] | {Id, RepoTags, RepoDigests}'
docker image inspect localhost/supply-chain-hardened | jq '.[0] | {Id, RepoTags, RepoDigests}'
```

Look closely at `RepoDigests`.

That is the immutable identity you want machines to rely on.

If you were writing a deployment reference for humans and machines together, prefer:

```text
localhost/supply-chain-hardened:latest@sha256:...
```

The tag can still communicate intent, but the digest is what locks the result.

## Part 3: Scan with Trivy

Start with the insecure image:

```sh
trivy image localhost/supply-chain-insecure
```

Now scan the hardened image:

```sh
trivy image localhost/supply-chain-hardened
```

Questions to answer:

- which image has more findings?
- are they OS package findings, app dependency findings, or both?
- do the findings say `fixed` or `unfixed`?

Export JSON for later:

```sh
trivy image --format json --output insecure-findings.json localhost/supply-chain-insecure
trivy image --format json --output hardened-findings.json localhost/supply-chain-hardened
```

Use `jq` to inspect counts:

```sh
jq '.Results | length' insecure-findings.json
jq '.Results[] | {Target, Class, Type}' insecure-findings.json
```

---

## Part 4: Generate an SBOM

Generate a CycloneDX SBOM for the hardened image:

```sh
trivy image --format cyclonedx --output hardened-sbom.json localhost/supply-chain-hardened
```

Inspect the SBOM:

```sh
jq '.bomFormat, .specVersion' hardened-sbom.json
jq '.components[:10] | map({name, version, type})' hardened-sbom.json
```

An SBOM tells you what is present.

It does **not** by itself prove:

- who built the image
- whether the build system was trusted
- whether the image was signed

---

## Part 5: CI/CD gates

Scan results become useful when a pipeline turns them into a rule.

Run the example gate:

```sh
./policy.sh localhost/supply-chain-insecure
./policy.sh localhost/supply-chain-hardened
```

Read the script:

```sh
cat policy.sh
```

The goal is not a perfect production policy. The point is to understand how:

- security signals become delivery decisions
- thresholds create tradeoffs
- teams need exceptions and review paths

---

## Part 6: What this course is really teaching

A software supply chain is not one tool.

It is the chain of trust from:

- source code
- commits
- build system
- base images
- dependencies
- image artifacts
- deployment policy

This is where `Git Signing` fits in conceptually:

- signed commits help you trust the source history
- image scanning helps you understand what you are shipping
- SBOMs help you inventory components
- policy gates decide whether release should proceed

## Key takeaway

When you need a reference that works for both humans and machines, prefer:

```text
image:tag@sha256:digest
```

That format keeps the tag for readability and the digest for immutability.

---

## Suggested review commands

```sh
docker images
docker image inspect localhost/supply-chain-insecure | jq '.[0] | {RepoTags, RepoDigests}'
trivy image localhost/supply-chain-insecure
trivy image --format json --output insecure-findings.json localhost/supply-chain-insecure
trivy image --format cyclonedx --output hardened-sbom.json localhost/supply-chain-hardened
jq '.components[:10]' hardened-sbom.json
./policy.sh localhost/supply-chain-insecure
./policy.sh localhost/supply-chain-hardened
```
