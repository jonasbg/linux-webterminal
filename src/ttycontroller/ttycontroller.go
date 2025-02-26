package ttycontroller

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/gorilla/websocket"
	"github.com/jonasbg/linux-terminal/m/v2/logger"
)

// TTYController manages Docker-based terminal sessions.
type TTYController struct {
	client            *client.Client
	sessions          map[string]*Session
	userSessions      map[string]string // Maps userID to sessionID
	mu                sync.Mutex
	logger            *logger.TTYLogger
	maxContainers     int
	containerLifetime int
}

type Shell struct {
	ID      string
	ExecID  string
	Conn    net.Conn
	Reader  io.Reader
	TabID   string
	LogFile string
	Done    chan struct{}
	Closed  bool
}

type Session struct {
	ContainerID string
	UserID      string
	Shells      map[string]*Shell
	WSConn      *websocket.Conn
	MainShellID string
}

// NewTTYController initializes a new TTYController instance.
func NewTTYController() (*TTYController, error) {
	cli, err := getDockerClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %v", err)
	}

	ctrl := &TTYController{
		client:            cli,
		sessions:          make(map[string]*Session),
		userSessions:      make(map[string]string),
		logger:            logger.NewTTYLogger(),
		maxContainers:     getEnvInt("MAX_CONTAINERS", 10),
		containerLifetime: getEnvInt("CONTAINER_LIFETIME", 3600),
	}
	ctrl.cleanupLeftoverContainers()
	return ctrl, nil
}

func getDockerClient() (*client.Client, error) {
	// Try different Docker socket locations
	socketPaths := []string{
		"unix:///var/run/docker.sock",                          // Standard Docker socket
		"unix://" + os.Getenv("HOME") + "/.colima/docker.sock", // Colima socket
		"unix:///run/podman/podman.sock",                       // Podman socket
		"unix:///run/user/1000/podman/podman.sock",             // Podman user socket
	}

	var lastErr error
	for _, socketPath := range socketPaths {
		client, err := client.NewClientWithOpts(
			client.WithHost(socketPath),
			client.WithAPIVersionNegotiation(),
		)
		if err == nil {
			// Test connection
			_, err = client.Ping(context.Background())
			if err == nil {
				log.Printf("Connected to Docker daemon at %s", socketPath)
				return client, nil
			}
		}
		lastErr = err
		log.Printf("Failed to connect to %s: %v", socketPath, err)
	}

	return nil, fmt.Errorf("could not connect to any Docker socket: %v", lastErr)
}

// GenerateUUID generates a unique identifier.
func GenerateUUID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()) // Simple unique ID; consider uuid package for UUIDv4
}

func (t *TTYController) GetMainShellID(sessionID string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if session, exists := t.sessions[sessionID]; exists && session.MainShellID != "" {
		return session.MainShellID, nil
	}
	return "", fmt.Errorf("no main shell ID found for session %s", sessionID)
}

func isPodman(cli *client.Client) bool {
	serverVersion, err := cli.ServerVersion(context.Background())
	if err != nil {
		return false
	}

	// Check Components array for Podman Engine
	for _, component := range serverVersion.Components {
		if strings.Contains(strings.ToLower(component.Name), "podman") {
			return true
		}
	}
	return false
}

// CreateSession creates a new session with an initial shell.
func (c *TTYController) CreateSession(sessionID, userID string, r *http.Request) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.sessions) >= c.maxContainers {
		return "", fmt.Errorf("maximum container limit (%d) reached", c.maxContainers)
	}

	if oldSessionID, exists := c.userSessions[userID]; exists {
		log.Printf("User %s has existing session %s, cleaning up", userID, oldSessionID)
		c.CleanupSession(oldSessionID)
	}

	image := os.Getenv("CONTAINER_IMAGE")
	if image == "" {
		image = "ghcr.io/jonasbg/linux-webterminal/terminal-base:latest"
	}

	ctx := context.Background()
	resp, err := c.client.ContainerCreate(ctx, &container.Config{
		Image:     image,
		Tty:       true,
		OpenStdin: true,
		User:      "1000:1000",
		Labels: map[string]string{
			"app":                      "web-terminal",
			"ws_id":                    sessionID,
			"user_id":                  userID,
			"io.containers.autoupdate": "image",
		},
		Env: []string{
			"TERM=xterm",
			"PS1=\\w \\$ ",
			"HOME=/home/termuser",
			"PATH=/usr/local/bin",
		},
	}, &container.HostConfig{
		AutoRemove: true,
		SecurityOpt: func() []string {
			securityOpts := []string{"no-new-privileges:true"}

			// Only add masks if running on Podman
			if isPodman(c.client) {
				masks := []string{
					"mask=/proc/cpuinfo",
					"mask=/proc/meminfo",
					"mask=/proc/diskstats",
					"mask=/proc/modules",
					"mask=/proc/kallsyms",
					"mask=/proc/keys",
					"mask=/proc/drivers",
					"mask=/proc/net",
					"mask=/proc/asound",
					"mask=/proc/key-users",
					"mask=/proc/slabinfo",
					"mask=/proc/uptime",
					"mask=/proc/stat",
					"mask=/proc/zoneinfo",
					"mask=/proc/vmallocinfo",
					"mask=/proc/mounts",
					"mask=/proc/kpageflags",
					"mask=/proc/kpagecount",
					"mask=/proc/kpagecgroup",
					"mask=/proc/scsi",
					"mask=/proc/buddyinfo",
					"mask=/proc/pagetypeinfo",
					"mask=/proc/ioports",
					"mask=/proc/iomem",
					"mask=/proc/interrupts",
					"mask=/proc/softirqs",
					"mask=/proc/dma",
					"mask=/proc/uptime",
				}
				securityOpts = append(securityOpts, masks...)
			}
			return securityOpts
		}(),
		CapDrop:     []string{"ALL"},
		NetworkMode: "none",
		Resources: container.Resources{
			Memory:    64 * 1024 * 1024, // 64MB
			CPUPeriod: 100000,
			CPUQuota:  10000,
			CPUShares: 128,
		},
		ReadonlyRootfs: true,
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeTmpfs,
				Target: "/tmp",
				TmpfsOptions: &mount.TmpfsOptions{
					SizeBytes: 64 * 1024 * 1024,
					Mode:      0755,
				},
			},
			{
				Type:   mount.TypeTmpfs,
				Target: "/home",
				TmpfsOptions: &mount.TmpfsOptions{
					SizeBytes: 64 * 1024 * 1024,
					Mode:      0755,
				},
			},
		},
	}, &network.NetworkingConfig{}, nil, "")
	if err != nil {
		return "", fmt.Errorf("failed to create container: %v", err)
	}

	if err := c.client.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		c.client.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{Force: true})
		return "", fmt.Errorf("failed to start container: %v", err)
	}

	session := &Session{
		ContainerID: resp.ID,
		UserID:      userID,
		Shells:      make(map[string]*Shell),
		WSConn:      nil, // Set later if needed
		MainShellID: "",  // Will be set after creating the shell
	}
	c.sessions[sessionID] = session
	c.userSessions[userID] = sessionID

	shellID, err := c.createShellInternal(sessionID, "initial", r)
	if err != nil {
		c.CleanupSession(sessionID)
		return "", err
	}

	// Store the main shell ID
	session.MainShellID = shellID

	go func() {
		time.Sleep(time.Duration(c.containerLifetime) * time.Second)
		c.CleanupSession(sessionID)
	}()

	log.Printf("Container %s created for session %s (user: %s, mainShellID: %s)", resp.ID[:12], sessionID, userID, shellID)
	return shellID, nil
}

// CreateShell creates a new shell within an existing session.
func (c *TTYController) CreateShell(sessionID, tabID string, r *http.Request) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.createShellInternal(sessionID, tabID, r)
}

type readResult struct {
	n    int
	data []byte
	err  error
}

// ResizeShell resizes a shell's terminal dimensions.
func (c *TTYController) ResizeShell(sessionID, shellID string, cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	session, ok := c.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found")
	}
	shell, ok := session.Shells[shellID]
	if !ok {
		return fmt.Errorf("shell not found")
	}

	ctx := context.Background()
	execConfig := types.ExecConfig{
		Cmd:          []string{"stty", fmt.Sprintf("cols %d rows %d", cols, rows)},
		AttachStdin:  true,
		AttachStdout: true,
		Tty:          true,
	}
	execID, err := c.client.ContainerExecCreate(ctx, session.ContainerID, execConfig)
	if err != nil {
		return err
	}
	if err := c.client.ContainerExecStart(ctx, execID.ID, types.ExecStartCheck{Tty: true}); err != nil {
		return err
	}

	return c.client.ContainerExecResize(ctx, shell.ExecID, types.ResizeOptions{
		Height: uint(rows),
		Width:  uint(cols),
	})
}

// CleanupSession cleans up a session and its resources.
func (c *TTYController) CleanupSession(sessionID string) {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	if !ok {
		c.mu.Unlock()
		return
	}

	userID := session.UserID
	containerID := session.ContainerID[:12]
	log.Printf("Cleaning up session %s for user %s (container: %s)", sessionID, userID, containerID)

	// First signal all shells that they're being closed
	for shellID, shell := range session.Shells {
		close(shell.Done)
		log.Printf("Signaled shell %s to close", shellID)
	}
	c.mu.Unlock()

	// Give shells time to clean up
	time.Sleep(300 * time.Millisecond)

	c.mu.Lock()
	// Check if session still exists
	session, ok = c.sessions[sessionID]
	if !ok {
		c.mu.Unlock()
		return
	}

	// Now close all connections
	for shellID, shell := range session.Shells {
		if shell.Conn != nil {
			shell.Conn.Close()
			log.Printf("Closed socket for shell %s", shellID)
		}
	}

	ctx := context.Background()
	if err := c.client.ContainerRemove(ctx, session.ContainerID, types.ContainerRemoveOptions{Force: true}); err != nil {
		log.Printf("Error removing container %s: %v", containerID, err)
	}

	delete(c.userSessions, userID)
	delete(c.sessions, sessionID)
	log.Printf("Session %s cleanup completed", sessionID)
	c.mu.Unlock()
}

// CleanupAllSessions cleans up all active sessions.
func (c *TTYController) CleanupAllSessions() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for sessionID := range c.sessions {
		c.CleanupSession(sessionID)
	}
	log.Println("All sessions cleaned up")
}

func (c *TTYController) cleanupLeftoverContainers() {
	ctx := context.Background()
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "app=web-terminal")
	containers, err := c.client.ContainerList(ctx, types.ContainerListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		log.Printf("Error listing containers: %v", err)
		return
	}

	for _, cont := range containers {
		// stopTimeout := 1
		// c.client.ContainerStop(ctx, cont.ID, types.ContainerStopOptions{Timeout: &stopTimeout})
		c.client.ContainerRemove(ctx, cont.ID, types.ContainerRemoveOptions{Force: true})
		log.Printf("Cleaned up leftover container: %s", cont.ID[:12])
	}
}

// WriteToShell without logging logic
func (c *TTYController) WriteToShell(sessionID, shellID, data string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	session, ok := c.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found")
	}
	shell, ok := session.Shells[shellID]
	if !ok {
		return fmt.Errorf("shell not found")
	}

	if shell.Conn == nil {
		return fmt.Errorf("shell connection not available")
	}

	// Use the logger to track commands instead
	c.logger.HandleInput(shellID, data)

	_, err := shell.Conn.Write([]byte(data))
	return err
}

// ReadShellOutput without logging logic
func (c *TTYController) ReadShellOutput(sessionID, shellID string, conn *websocket.Conn) {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	if !ok {
		c.mu.Unlock()
		log.Printf("Session %s not found in ReadShellOutput", sessionID)
		return
	}
	shell, ok := session.Shells[shellID]
	if !ok {
		c.mu.Unlock()
		log.Printf("Shell %s not found in session %s", shellID, sessionID)
		return
	}
	reader := shell.Reader
	done := shell.Done // Get a reference to the Done channel

	// Check if this is the main shell
	isMainShell := (shellID == session.MainShellID || shellID == "main")
	c.mu.Unlock()

	// Use a separate goroutine to read from the shell
	readCh := make(chan readResult)
	readerClosed := make(chan struct{})

	go func() {
		defer close(readerClosed)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if err != nil {
				readCh <- readResult{n: 0, err: err}
				return
			}
			readCh <- readResult{n: n, data: buf[:n]}
		}
	}()

	for {
		select {
		case <-done:
			// Shell is being closed externally, exit gracefully
			log.Printf("Reader for shell %s received done signal, exiting", shellID)
			// Wait for reader goroutine to finish
			<-readerClosed
			return

		case result := <-readCh:
			if result.err != nil {
				log.Printf("Error reading from shell %s: %v", shellID, result.err)

				// If the shell process exited, notify frontend but don't call CloseShell here
				if result.err == io.EOF {
					log.Printf("Shell %s exited normally with EOF", shellID)

					// Send shell_closed notification to frontend
					err := conn.WriteJSON(map[string]interface{}{
						"type": "shell_closed",
						"data": map[string]string{
							"shell_id":     shellID,
							"container_id": sessionID,
						},
					})
					if err != nil {
						log.Printf("Error sending shell_closed notification: %v", err)
					}

					// Mark this shell as closed WITHOUT using CloseShell directly
					go func() {
						c.mu.Lock()
						defer c.mu.Unlock()

						session, ok := c.sessions[sessionID]
						if !ok {
							return
						}

						// Just mark the shell as closed by removing it from the map
						delete(session.Shells, shellID)
						log.Printf("Shell %s removed from session after EOF", shellID)
					}()
				}

				// Wait for reader goroutine to finish
				<-readerClosed
				return
			}

			output := string(result.data)

			// Pass output to logger for command tracking and logging
			c.logger.HandleOutput(shellID, output)

			// Use different message format depending on if this is the main shell or not
			var messageErr error
			if isMainShell {
				// For the main shell, use the "output" message type
				messageErr = conn.WriteJSON(map[string]interface{}{
					"type": "output",
					"data": map[string]string{
						"output": output,
					},
				})
			} else {
				// For additional shells, use the "shell_output" message type
				messageErr = conn.WriteJSON(map[string]interface{}{
					"type": "shell_output",
					"data": map[string]string{
						"shell_id": shellID,
						"output":   output,
					},
				})
			}

			if messageErr != nil {
				log.Printf("Failed to send output: %v", messageErr)
				return
			}
		}
	}
}

// CloseShell with simplified logging
func (c *TTYController) CloseShell(sessionID, shellID string) {
	// First, check if the shell exists
	var shell *Shell
	var ok bool

	c.mu.Lock()
	session, sessionOk := c.sessions[sessionID]
	if sessionOk {
		shell, ok = session.Shells[shellID]
	}

	if !sessionOk || !ok {
		c.mu.Unlock()
		log.Printf("Shell %s not found in session %s for closing", shellID, sessionID)
		return
	}

	// Signal to the reader goroutine that we're closing
	// Only close the channel if it hasn't been closed already
	select {
	case <-shell.Done:
		// Already closed
	default:
		close(shell.Done)
	}

	// Get a reference to the connection before unlocking
	conn := shell.Conn
	c.mu.Unlock()

	// These operations don't need the lock
	if conn != nil {
		// Try to send exit command
		// Don't check for errors since the connection might already be closing
		conn.Write([]byte{0x04}) // Ctrl+D
		time.Sleep(50 * time.Millisecond)
		conn.Write([]byte("exit\r\n"))
		time.Sleep(50 * time.Millisecond)
		conn.Close()
	}

	// Notify logger that shell is closing
	c.logger.CleanupShell(shellID)

	// Give things time to clean up
	time.Sleep(100 * time.Millisecond)

	// Now remove the shell from the session
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check again if session still exists
	if session, ok = c.sessions[sessionID]; ok {
		delete(session.Shells, shellID)
		log.Printf("Shell %s removed from session by CloseShell", shellID)
	}
}

// Update the CreateShell/createShellInternal method
func (c *TTYController) createShellInternal(sessionID, tabID string, r *http.Request) (string, error) {
	session, ok := c.sessions[sessionID]
	if !ok {
		return "", fmt.Errorf("session %s not found", sessionID)
	}

	ctx := context.Background()

	// Set initial terminal size
	execConfig := types.ExecConfig{
		Cmd:          []string{"stty", "cols", "142"},
		AttachStdin:  true,
		AttachStdout: true,
		Tty:          true,
	}
	execID, err := c.client.ContainerExecCreate(ctx, session.ContainerID, execConfig)
	if err != nil {
		return "", fmt.Errorf("failed to set terminal size: %v", err)
	}
	if err := c.client.ContainerExecStart(ctx, execID.ID, types.ExecStartCheck{Tty: true}); err != nil {
		return "", fmt.Errorf("failed to start stty exec: %v", err)
	}

	// Create shell exec instance
	execConfig = types.ExecConfig{
		Cmd:          []string{"/usr/local/bin/sh", "-l"},
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	}
	execID, err = c.client.ContainerExecCreate(ctx, session.ContainerID, execConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create exec instance: %v", err)
	}

	resp, err := c.client.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{Tty: true})
	if err != nil {
		return "", fmt.Errorf("failed to attach to exec: %v", err)
	}

	// Use "main" as the shellID for the initial shell
	var shellID string
	if tabID == "initial" {
		shellID = "main"
	} else {
		shellID = fmt.Sprintf("shell-%s", GenerateUUID())
	}

	// Create log file through the logger
	logFile := c.logger.CreateSessionLog(session.ContainerID, session.UserID, shellID, r)

	shell := &Shell{
		ID:      shellID,
		ExecID:  execID.ID,
		Conn:    resp.Conn,
		Reader:  resp.Reader,
		TabID:   tabID,
		LogFile: logFile,
		Done:    make(chan struct{}),
		Closed:  false,
	}
	session.Shells[shellID] = shell

	log.Printf("Shell %s created for session %s", shellID, sessionID)
	return shellID, nil
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}
