---
difficulty: Intermediate
duration: 45-60 min
tags: [git, gpg, ssh, security, signing]
---

# Linux II - Git Commit Signing

Learn how to verify that code really comes from who it claims to.

## What you'll learn

- The git author email is just a string - **anyone can set it to anything**. It is not proof of identity.
- How GPG and SSH keys provide real cryptographic proof that a commit was made by a specific person
- Inspect signed vs unsigned commits in a real repository
- Understand the `allowed_signers` file and trust model
- Configure git to sign your own commits
- Why commit signing matters for supply chain security

## Why this matters

Run `git config user.email "ceo@company.com"` and your next commit looks like it came from the CEO. Git does not verify the author field - it trusts whatever you type. Without commit signing, there is no way to distinguish a legitimate commit from an impersonated one.

Signed commits solve this by attaching a cryptographic signature that can only be produced by someone holding the private key.

## The setup

Start with `cat warmup.md` - a quick exercise where you impersonate Linus Torvalds in a git commit and see how easy it is. Then move on to `instructions.md` for the main investigation: a real git repository in `signing-project/` with a mix of signed and unsigned commits, some of which may not be from who they claim. The `cheatsheet.md` has all the commands you'll need.

## Who is this for?

Developers who use git daily but haven't set up commit signing yet. You should be comfortable with basic git commands (`log`, `show`, `diff`).
