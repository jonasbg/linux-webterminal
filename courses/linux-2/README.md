---
difficulty: Intermediate
duration: 45-60 min
tags: [git, gpg, ssh, security, signing]
---

# Linux II - Git Commit Signing

Learn how to verify that code really comes from who it claims to.

## What you'll learn

- How GPG and SSH keys work for signing git commits
- Inspect signed vs unsigned commits in a real repository
- Understand the `allowed_signers` file and trust model
- Configure git to sign your own commits
- Why commit signing matters for supply chain security

## The setup

You have a real git repository in `signing-project/` with a mix of signed and unsigned commits. The `instructions.md` and `cheatsheet.md` files walk you through everything step by step.

## Who is this for?

Developers who use git daily but haven't set up commit signing yet. You should be comfortable with basic git commands (`log`, `show`, `diff`).
