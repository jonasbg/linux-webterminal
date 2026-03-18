---
difficulty: Intermediate
duration: 60-90 min
tags: [linux, proc, processes, namespaces, debugging]
---

# Linux III - Process Investigation

Look under the hood of a running Linux system through `/proc`.

## What you'll learn

- How Linux represents every process as files in `/proc`
- Inspect PIDs, command lines, environment variables, and memory maps
- Understand parent-child process relationships
- Work with file descriptors - the foundation of pipes and redirection
- Discover how PID namespaces provide container isolation
- Spot secrets leaked through environment variables

## The exercises

Follow `instruction.md` through 7 guided parts. You'll create processes, inspect them through `/proc`, and understand how Linux manages everything from memory to process trees.

## Who is this for?

Anyone who wants to understand what's really happening when you run a command. Useful for developers debugging applications, ops people investigating issues, or anyone preparing for container/cloud work.
