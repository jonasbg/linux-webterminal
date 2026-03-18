# LINUX PROCESS INVESTIGATION

```
────┬─────────┬─────────┬───┬─────────┬─────────────────────┬───────┬─────────┬─────┬─────────┬───┐
S   │         │         │   │         │                     │       │         │     │         │   │
┌─┐ ├──── ┌─┐ │ ──────┐ │ │ │ │ ┌───┐ │ │ ┌───────────┬───┐ │ ──┬───┘ ──┬── │ │ ──┬─┘ ┌───┐ │ │ │ │
│ │ │     │ │ │       │ │ │ │ │ │   │ │ │ │           │   │ │   │       │   │ │   │   │   │ │   │ │
│ │ │ ┌───┘ │ └─────┐ └─┤ │ │ │ │ │ │ │ │ │ ──┬───┬── │ ──┘ └── │ ──┬───┘ ┌─┤ ├── │ ──┴── │ ├───┴─┤
│ │ │ │             │   │ │   │   │ │   │ │   │   │   │         │   │     │ │ │   │       │ │     │
│ │ │ └─────────┬─┐ └─┐ │ └─┬─┴───┘ ├───┼─┴─┐ │ │ │ ──┴───────┬─┴── │ ┌───┘ │ │ ┌─┴─┬──── │ │ ──┐ │
│ │ │           │ │   │     │       │   │   │   │ │           │     │ │     │   │   │     │     │ │
│ │ └─────┐ ┌── │ └─┐ └─┬── │ ──────┘ │ │ │ │ ┌─┴─┤ ┌───────┐ │ ┌───┘ │ │ ┌─┴─┐ │ │ │ ┌─┬─┴─┬───┤ │
│ │       │ │   │   │   │   │         │   │   │   │ │       │   │     │ │ │   │ │ │ │ │ │   │   │ │
│ └─┬───┐ │ │ ┌─┘ ──┴─┐ └─┬─┴───────┬─┴─┐ ├───┘ │ └─┤ ──┬─┐ └───┤ ────┤ │ │ │ │ │ │ │ │ │ │ │ │ │ │
│   │   │ │ │ │       │   │         │   │ │     │   │   │ │     │     │ │   │   │ │ │ │ │ │   │   │
│ │ └── │ └─┘ ├──── ┌─┴─┐ │ ──┬──── │ │ └─┤ ────┼── ├── │ └───┐ └─┐ │ └─┴───┴───┘ │ │ │ │ └───┴───┤
│ │     │     │     │   │ │   │     │ │   │     │   │   │     │   │ │             │     │         │
│ └─────┴─────┘ ────┘ │ │ └── │ ────┘ ├── └──── │ ──┘ ┌─┘ │ ──┴── └─┴─────────────┴─────┴──────── │
│                     │       │       │         │     │   │                                       E
└─────────────────────┴───────┴───────┴─────────┴─────┴───┴────────────────────────────────────────
```

## MISSION BRIEF

As systems administrators, understanding how processes work in Linux is crucial for debugging, monitoring, and security analysis. In this task, you'll explore the `/proc` virtual filesystem, which provides a window into the kernel's view of processes running on your system.

## OBJECTIVES

1. Understand the structure and purpose of the `/proc` filesystem
2. Learn how to inspect process information including PID, command line args, and environment variables
3. Create and monitor processes to understand their lifecycle
4. Explore process isolation mechanisms in Linux (namespaces, cgroups)

## BACKGROUND

The `/proc` filesystem is a virtual filesystem that doesn't exist on disk but is created in memory by the Linux kernel. It provides a window into the kernel's internals and process information. Each running process has its own directory named after its Process ID (PID).

## INSTRUCTIONS

### PART 1: Exploring Your Shell Process

1. Start by identifying your current shell's PID:
   ```
   echo $$
   ```

2. Explore your shell's process directory:
   ```
   ls -la /proc/$$
   ```

3. Examine your process's command line:
   ```
   cat /proc/$$/cmdline | tr '\0' ' '
   ```

   Note: The `tr '\0' ' '` command replaces null bytes (used as separators in cmdline) with spaces for readability.

4. Look at your process's environment variables:
   ```
   cat /proc/$$/environ | tr '\0' '\n'
   ```

   Note: Environment variables are stored as null-terminated strings, so we convert null bytes to newlines for readability.

5. Check your process status:
   ```
   cat /proc/$$/status
   ```

   This file contains key information about the process in a human-readable format.

### PART 2: Creating a Long-Running Process

1. Start a long-running process in the background:
   ```
   sleep 600 &
   ```

2. Note the PID that was printed after starting the process.

3. Verify the process is running:
   ```
   ps | grep sleep
   ```

   This command combines two utilities:
   - `ps` shows the current running processes
   - `grep sleep` filters the output to show only lines containing "sleep"

   You'll typically see two lines: one for your actual sleep process and another for the grep command itself.

4. Explore this new process:
   ```
   ls -la /proc/[PID]  # Replace [PID] with the actual PID
   ```

5. Compare its command line with your shell:
   ```
   cat /proc/[PID]/cmdline | tr '\0' ' '
   ```

### PART 3: Process Relationships

1. Examine your shell's parent process:
   ```
   cat /proc/$$/status | grep PPid
   ```

   The PPid (Parent Process ID) shows which process created your current shell.

2. Spawn a new shell and inspect your parent process again:
   ```
   sh
   cat /proc/$$/status | grep PPid
   ```

   Notice how the parent PID changed - your new shell's parent is your previous shell!

3. Check the process tree:
   ```
   cat /proc/$$/task/$$/children
   ```

   This shows the PIDs of all direct child processes of your shell.

### PART 4: Process Resource Usage & Memory

1. Check memory usage of your sleep process:
   ```
   cat /proc/[PID]/status | grep -i vm
   ```

   You'll see output similar to:
   ```
   VmPeak:     1740 kB  # Peak virtual memory size ever used
   VmSize:     1712 kB  # Current virtual memory size allocated
   VmLck:         0 kB  # Memory locked and cannot be swapped out
   VmPin:         0 kB  # Pinned memory that must stay in RAM
   VmHWM:      1236 kB  # "High Water Mark" - peak resident set size
   VmRSS:      1236 kB  # Resident Set Size - physical memory actually used
   VmData:      112 kB  # Size of data segment
   VmStk:       132 kB  # Size of stack segment
   VmExe:       616 kB  # Size of text segment (executable code)
   VmLib:       356 kB  # Size of shared libraries
   VmPTE:        40 kB  # Page Table Entries size
   VmSwap:        0 kB  # Swap space used
   ```

   These values reveal how memory is allocated to the process:
   - The difference between VmSize and VmRSS shows memory that's allocated but not used
   - VmData and VmStk show how much memory is used for variables vs. function calls
   - VmExe shows the size of the actual program code

2. Examine file descriptors:
   ```
   ls -la /proc/[PID]/fd
   ```

   This shows all open files and connections for the process.

### PART 5: Process Environment Variables

1. Start a process with a custom environment variable:
   ```
   SECRET=s3cr37 sleep 600 &
   ```

2. Use the special `$!` variable to get the PID of the last background process:
   ```
   echo "Process ID of sleep: $!"
   ```

   The `$!` shell variable always contains the PID of the most recently executed background process.

3. Save the PID for easy reference:
   ```
   SLEEP_PID=$!
   ```

4. Examine the environment variables of this process:
   ```
   cat /proc/$SLEEP_PID/environ | tr '\0' '\n'
   ```

5. Notice how the `SECRET` variable was passed to the process and is readable from another process.

   This is an important security consideration - environment variables are visible to anyone who can read the /proc filesystem!

#### PART 5.2: Environment variables

1. Create a new environment:
   ```
   PARENT_ENV="from parent"
   ```

2. Create a new child process:
   ```
   sleep 600 &
   ```

3. View the environments for this process:
   ```
   echo $!
   cat /proc/$!/environ
   ```

   > Is PARENT_ENV available to the child process? What did you observe?

4. Create a new exported variable:
   ```
   export EXPORTED_VARIABLE="this variable is exported"
   ```

5. Create a new child process:
   ```
   sleep 600 &
   ```

6. View the environments for this child process:
   ```
   cat /proc/$!/environ
   ```

   > Is EXPORTED_VARIABLE available to the child process? What did you observe?


### PART 6: File Descriptors - The Magic Numbers

File descriptors are how Linux represents open files, sockets, pipes, and other I/O resources. Understanding them reveals how processes interact with the outside world.

1. List all open file descriptors for your shell:
   ```
   ls -la /proc/$$/fd
   ```

   You'll typically see:
   - 0: stdin (standard input)
   - 1: stdout (standard output)
   - 2: stderr (standard error)

   These are the three default file descriptors every process starts with.

2. Create a file and observe how it appears in your file descriptors:
   ```
   exec 3>tempfile.txt
   ls -la /proc/$$/fd
   echo "This goes to fd 3" >&3
   cat tempfile.txt
   ```

   What's happening:
   - `exec 3>tempfile.txt` opens tempfile.txt and assigns it to file descriptor 3
   - When you list fd directory again, you'll see a new entry for fd 3
   - `echo "This goes to fd 3" >&3` redirects output to fd 3 instead of stdout
   - The text appears in the file rather than on screen

   This demonstrates how you can manipulate I/O channels in Linux, which is the foundation of pipelines and redirection.

### PART 7: The Mysterious /proc/self

1. The `/proc/self` directory is a special symlink that always points to the current process:
   ```
   ls -la /proc/self
   readlink /proc/self
   ```

   `/proc/self` is a "magical" symlink that the kernel automatically resolves to the process reading it.

2. Run the same command multiple times and observe that it changes:
   ```
   echo "My PID is $(readlink /proc/self)"
   echo "My PID is $(readlink /proc/self)"
   ```

   Each time you run the command, a new process is created with a new PID, so `/proc/self` resolves differently.

3. Create a one-liner that reads its own memory map:
   ```
   echo "I'm reading my own memory map: $(head -1 /proc/self/maps)"
   ```

   This is self-referential access - the process is reading information about itself through `/proc/self`.
   When the command runs, the shell creates a subshell for the command substitution,
   which accesses its own memory map through `/proc/self/maps`.

## UNDERSTANDING CONTAINER PID NAMESPACES

When working in this environment, you'll notice something interesting: every container instance has processes with the same PIDs (typically with PID 1 and 3 being most prominent). This is not a coincidence!

This happens because:

1. **PID Namespace Isolation**: Each container gets its own isolated PID namespace. This means processes inside each container have their own set of PIDs, completely separate from the host system or other containers.

2. **Container Process Hierarchy**:
   - PID 1 is always the container's init process
   - PID 3 is typically your shell process (the /usr/local/bin/sh -l that you're interacting with)

3. **Security Benefits**: This isolation prevents processes in one container from seeing or interacting with processes in other containers or the host system.

4. **Consistent Startup Sequence**: The container startup process follows the same sequence every time, leading to consistent PID assignments.

This behavior is a fundamental feature of container technology, providing strong isolation between containers while allowing them to believe they have a complete system to themselves.

## CHALLENGES

1. Start two sleep processes with different durations. Compare their `/proc` entries.
2. Try to find information about which user is running a specific process.
3. Create a process with multiple environment variables and examine how they appear in `/proc/[PID]/environ`.
4. Try redirecting both stdout and stderr to different files using file descriptors.
5. Create a script that uses `/proc/self` to report its own resource usage.
6. Create a process and then try to find all references to this process in the entire `/proc` filesystem.
7. Compare the memory map of your shell with that of a sleep process. What differences do you notice?

## QUESTIONS TO ANSWER

1. What is the relationship between the PIDs you observe and how processes are created in a containerized environment?
2. How do the command line arguments in `/proc/[PID]/cmdline` differ from what you see in `ps`?
3. What isolation mechanisms can you identify from examining the namespace information?
4. How are cgroups being used to limit resources in this container environment?
5. If you were debugging a misbehaving application, which `/proc` files would be most useful to examine?
6. Why might having isolated PID namespaces be important for security in multi-tenant environments?

## CONCLUSION

Understanding the `/proc` filesystem provides deep insights into how Linux manages processes. This knowledge is invaluable for system administration, debugging, and security analysis.

When you've completed this exercise, you'll have hands-on experience with process inspection, monitoring, and understanding the Linux process model.
