package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Helper function to create pointers to primitives
func pointer[T any](v T) *T {
	return &v
}

// RequestMetadata handles IP address detection through proxies
type RequestMetadata struct {
	// Known proxy headers in order of preference
	ProxyHeaders []string
}

// NewRequestMetadata creates a new RequestMetadata instance
func NewRequestMetadata() *RequestMetadata {
	return &RequestMetadata{
		ProxyHeaders: []string{
			"CF-Connecting-IP",         // Cloudflare
			"X-Forwarded-For",          // General proxy header
			"X-Real-IP",                // Nginx
			"X-Original-Forwarded-For", // Modified forwarded header
			"Forwarded",                // RFC 7239
			"True-Client-IP",           // Akamai and others
			"X-Client-IP",              // Various proxies
		},
	}
}

// GetClientIP extracts client IP from request considering various proxy headers
func (rm *RequestMetadata) GetClientIP(r *http.Request) (string, string) {
	// Check Cloudflare and other proxy headers first
	for _, header := range rm.ProxyHeaders {
		if value := r.Header.Get(header); value != "" {
			// Handle X-Forwarded-For chains (take the first IP)
			if header == "X-Forwarded-For" {
				ips := strings.Split(value, ",")
				return strings.TrimSpace(ips[0]), header
			}
			// Handle RFC 7239 Forwarded header
			if header == "Forwarded" {
				parts := strings.Split(value, ";")
				for _, part := range parts {
					if strings.HasPrefix(strings.TrimSpace(part), "for=") {
						// Remove possible IPv6 brackets and port
						ipPart := strings.Split(part, "=")[1]
						ipPart = strings.Trim(ipPart, "\"[]")
						ip := strings.Split(ipPart, ":")[0]
						return ip, header
					}
				}
			}
			return value, header
		}
	}

	// Fall back to direct IP
	return r.RemoteAddr, "direct"
}

// GetUserAgent extracts and normalizes user agent information
func (rm *RequestMetadata) GetUserAgent(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		ua = "Unknown"
	}
	return ua
}

// GetRequestMetadata gets complete request metadata including IP and user agent
func (rm *RequestMetadata) GetRequestMetadata(r *http.Request) map[string]interface{} {
	ip, ipSource := rm.GetClientIP(r)
	userAgent := rm.GetUserAgent(r)

	// Convert headers to map
	headers := make(map[string]string)
	for key, values := range r.Header {
		headers[key] = strings.Join(values, ", ")
	}

	return map[string]interface{}{
		"ip_address": ip,
		"ip_source":  ipSource,
		"user_agent": userAgent,
		"headers":    headers,
	}
}

// TTYLogger logs terminal sessions and interactions
type TTYLogger struct {
	enabled bool
	logDir  string
	mutex   sync.Mutex
}

// NewTTYLogger creates a new TTYLogger instance
func NewTTYLogger() *TTYLogger {
	enabled := strings.ToLower(os.Getenv("TTY_LOGGING_ENABLED")) == "true"
	logDir := os.Getenv("TTY_LOG_DIR")
	if logDir == "" {
		logDir = "./logs"
	}

	logger := &TTYLogger{
		enabled: enabled,
		logDir:  logDir,
	}

	if enabled {
		err := os.MkdirAll(logDir, 0755)
		if err != nil {
			log.Printf("Failed to create log directory: %v", err)
		}
	}

	return logger
}

// CreateSessionLog creates a new log file for a terminal session
func (tl *TTYLogger) CreateSessionLog(containerID, userID, wsID string, r *http.Request) string {
	if !tl.enabled {
		return ""
	}

	tl.mutex.Lock()
	defer tl.mutex.Unlock()

	timestamp := time.Now().Format("20060102-150405")
	filename := filepath.Join(tl.logDir, fmt.Sprintf("%s-%s.log", timestamp, containerID[:12]))

	file, err := os.Create(filename)
	if err != nil {
		log.Printf("Failed to create log file: %v", err)
		return ""
	}
	defer file.Close()

	fmt.Fprintf(file, "Session Start: %s\n", timestamp)
	fmt.Fprintf(file, "Container ID: %s\n", containerID)
	fmt.Fprintf(file, "User ID: %s\n", userID)
	fmt.Fprintf(file, "WebSocket ID: %s\n", wsID)

	if r != nil {
		rm := NewRequestMetadata()
		metadata := rm.GetRequestMetadata(r)
		fmt.Fprintf(file, "Origin IP: %s (via %s)\n",
			metadata["ip_address"], metadata["ip_source"])
		fmt.Fprintf(file, "User Agent: %s\n", metadata["user_agent"])
	} else {
		fmt.Fprintf(file, "Origin IP: Not available\n")
		fmt.Fprintf(file, "User Agent: Not available\n")
	}

	fmt.Fprintf(file, "\n=== Command History ===\n\n")

	return filename
}

// LogCommand logs a command and its output to the specified log file
func (tl *TTYLogger) LogCommand(filename, command, output string) {
	if !tl.enabled || filename == "" {
		return
	}

	tl.mutex.Lock()
	defer tl.mutex.Unlock()

	timestamp := time.Now().Format("15:04:05")

	file, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file: %v", err)
		return
	}
	defer file.Close()

	fmt.Fprintf(file, "[%s] > %s\n", timestamp, strings.TrimSpace(command))
	if output != "" {
		cleanedOutput := tl.CleanTerminalOutput(output, command)
		if strings.TrimSpace(cleanedOutput) != "" {
			fmt.Fprintf(file, "%s\n", strings.TrimSpace(cleanedOutput))
		}
	}
}

// CleanTerminalOutput removes terminal control sequences and formatting
func (tl *TTYLogger) CleanTerminalOutput(text, command string) string {
	// Strip ANSI color codes
	ansiPattern := `\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`
	ansiRE := regexp.MustCompile(ansiPattern)
	text = ansiRE.ReplaceAllString(text, "")

	// Remove specific control sequences
	text = strings.ReplaceAll(text, "\x1b[6n", "") // DSR
	text = strings.ReplaceAll(text, "\x1b[J", "")  // Clear screen
	text = strings.ReplaceAll(text, "\x1b[K", "")  // Clear line
	text = strings.ReplaceAll(text, "\x07", "")    // Bell
	text = strings.ReplaceAll(text, "\a", "")      // Bell alternative

	// Convert carriage returns to newlines
	text = strings.ReplaceAll(text, "\r", "\n")

	// Remove terminal prompt lines
	promptPattern := `termuser@container.*\$|~\$`
	promptRE := regexp.MustCompile(promptPattern)

	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if !promptRE.MatchString(line) {
			// Handle backspace characters
			for strings.Contains(line, "\b") {
				line = regexp.MustCompile(".\b").ReplaceAllString(line, "")
			}
			lines = append(lines, line)
		}
	}

	text = strings.Join(lines, "\n")

	// Remove the command from the beginning of the output
	if command != "" {
		cmdPattern := fmt.Sprintf("^%s", regexp.QuoteMeta(strings.TrimSpace(command)))
		cmdRE := regexp.MustCompile(cmdPattern)
		text = cmdRE.ReplaceAllString(text, "")
	}

	// Trim whitespace
	text = strings.TrimSpace(text)

	// Collapse multiple consecutive empty lines
	text = regexp.MustCompile(`\n\s*\n\s*\n+`).ReplaceAllString(text, "\n\n")

	return text
}

// ShellInfo represents information about a shell instance
type ShellInfo struct {
	ExecID           string
	HijackedResponse *types.HijackedResponse
	TabID            string
	LogFile          string
	CurrentCommand   string
	Buffer           string
	PendingCommand   string
	OutputBuffer     string
	CommandStartTime time.Time
	CommandComplete  bool
}

// SessionInfo represents a session with container and shells
type SessionInfo struct {
	ContainerID     string
	UserID          string
	LogFile         string
	CurrentCommand  string
	Buffer          string
	CommandComplete bool
	MainShellID     string
	Shells          map[string]*ShellInfo
}

// TTYController manages Docker container-based terminal sessions
type TTYController struct {
	client            *client.Client
	sessions          map[string]*SessionInfo
	userSessions      map[string]string
	lock              sync.RWMutex
	logger            *TTYLogger
	maxContainers     int
	containerLifetime int
}

// NewTTYController creates a new TTYController instance
func NewTTYController() (*TTYController, error) {
	// Get Docker client
	dockerClient, err := getDockerClient()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %v", err)
	}

	// Get limits from environment
	maxContainers, err := strconv.Atoi(os.Getenv("MAX_CONTAINERS"))
	if err != nil || maxContainers <= 0 {
		maxContainers = 10
	}

	containerLifetime, err := strconv.Atoi(os.Getenv("CONTAINER_LIFETIME"))
	if err != nil || containerLifetime <= 0 {
		containerLifetime = 3600
	}

	tc := &TTYController{
		client:            dockerClient,
		sessions:          make(map[string]*SessionInfo),
		userSessions:      make(map[string]string),
		logger:            NewTTYLogger(),
		maxContainers:     maxContainers,
		containerLifetime: containerLifetime,
	}

	// Clean up any leftover containers
	tc.cleanupLeftoverContainers()

	return tc, nil
}

// getDockerClient creates a Docker client
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

// cleanupLeftoverContainers cleans up any containers with our label
func (tc *TTYController) cleanupLeftoverContainers() {
	containers, err := tc.client.ContainerList(context.Background(), types.ContainerListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", "app=web-terminal")),
	})
	if err != nil {
		log.Printf("Error listing containers for cleanup: %v", err)
		return
	}

	log.Printf("Found %d leftover containers to clean up", len(containers))
	for _, c := range containers {
		log.Printf("Cleaning up leftover container: %s", c.ID[:12])
		err := tc.client.ContainerStop(context.Background(), c.ID, pointer(time.Second))
		if err != nil {
			log.Printf("Error stopping container %s: %v", c.ID[:12], err)
		}

		err = tc.client.ContainerRemove(context.Background(), c.ID, types.ContainerRemoveOptions{
			Force: true,
		})
		if err != nil {
			log.Printf("Error removing container %s: %v", c.ID[:12], err)
		}
	}
}

// CreateSessionWithShells creates a new container session with shell support
func (tc *TTYController) CreateSessionWithShells(wsID, userID string, r *http.Request) (string, string, error) {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	log.Printf("Creating new session for user %s (ws_id: %s)", userID, wsID)

	// Check if we've hit the container limit
	if len(tc.sessions) >= tc.maxContainers {
		log.Printf("Maximum container limit (%d) reached", tc.maxContainers)
		return "", "", fmt.Errorf("maximum number of containers reached")
	}

	// Check if user has an existing session
	if oldWsID, ok := tc.userSessions[userID]; ok {
		log.Printf("User %s has existing session %s, cleaning up", userID, oldWsID)
		tc.CleanupSession(oldWsID)
	}

	// Create container
	image := os.Getenv("CONTAINER_IMAGE")
	if image == "" {
		image = "ghcr.io/jonasbg/linux-webterminal/terminal-base:latest"
	}

	log.Printf("Starting container with image: %s", image)

	// Security options
	securityOpts := []string{
		"no-new-privileges:true",
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
		// Additional mask options can be added here
	}

	// Create container config
	config := &container.Config{
		Image:     image,
		Tty:       true,
		OpenStdin: true,
		User:      "1000:1000",
		Labels: map[string]string{
			"app":                      "web-terminal",
			"ws_id":                    wsID,
			"user_id":                  userID,
			"io.containers.autoupdate": "image",
		},
		Env: []string{
			"TERM=xterm",
			"PS1=\\w \\$ ",
			"HOME=/home/termuser",
			"PATH=/usr/local/bin",
		},
	}

	// Host config with resource limits and security settings
	hostConfig := &container.HostConfig{
		SecurityOpt: securityOpts,
		NetworkMode: "none",
		CapDrop:     []string{"ALL"},
		Resources: container.Resources{
			Memory:    67108864, // 64MB
			CPUPeriod: 100000,
			CPUQuota:  10000,
			CPUShares: 128,
			PidsLimit: pointer(int64(10)),
		},
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/tmp":  "size=64m,noexec,nosuid",
			"/home": "size=64m,exec",
		},
	}

	// Create the container
	cont, err := tc.client.ContainerCreate(
		context.Background(),
		config,
		hostConfig,
		nil,
		nil,
		"",
	)
	if err != nil {
		log.Printf("Failed to create container: %v", err)
		return "", "", err
	}

	// Start the container
	err = tc.client.ContainerStart(context.Background(), cont.ID, types.ContainerStartOptions{})
	if err != nil {
		log.Printf("Failed to start container: %v", err)
		tc.client.ContainerRemove(context.Background(), cont.ID, types.ContainerRemoveOptions{Force: true})
		return "", "", err
	}

	// Initialize the session structure
	tc.sessions[wsID] = &SessionInfo{
		ContainerID: cont.ID,
		UserID:      userID,
		Shells:      make(map[string]*ShellInfo),
	}
	tc.userSessions[userID] = wsID

	// Create the main shell for this container
	shellID, err := tc.CreateShell(wsID, "initial", userID, r)
	if err != nil {
		log.Printf("Failed to create main shell: %v", err)
		tc.CleanupSession(wsID)
		return "", "", err
	}

	// Store the main shell ID
	tc.sessions[wsID].MainShellID = shellID

	// Start cleanup timer
	go func() {
		time.Sleep(time.Duration(tc.containerLifetime) * time.Second)
		tc.CleanupSession(wsID)
	}()

	log.Printf("Container %s created successfully for user %s", cont.ID[:12], userID)
	return cont.ID, shellID, nil
}

// CreateShell creates a new shell in an existing container session
func (tc *TTYController) CreateShell(wsID, tabID, userID string, r *http.Request) (string, error) {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	log.Printf("Creating new shell for session %s (tab: %s, user: %s)", wsID, tabID, userID)

	session, ok := tc.sessions[wsID]
	if !ok {
		log.Printf("Session %s not found", wsID)
		return "", fmt.Errorf("session not found: %s", wsID)
	}

	// First set the terminal size
	execConfig := types.ExecConfig{
		Cmd:          []string{"stty", "cols", "142"},
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	}

	exec, err := tc.client.ContainerExecCreate(context.Background(), session.ContainerID, execConfig)
	if err != nil {
		log.Printf("Failed to create stty exec: %v", err)
		return "", err
	}

	startCheck := types.ExecStartCheck{
		Tty: true,
	}

	err = tc.client.ContainerExecStart(context.Background(), exec.ID, startCheck)
	if err != nil {
		log.Printf("Failed to start stty exec: %v", err)
		return "", err
	}

	// Create shell exec
	shellConfig := types.ExecConfig{
		Cmd:          []string{"/usr/local/bin/sh", "-l"},
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	}

	shellExec, err := tc.client.ContainerExecCreate(context.Background(), session.ContainerID, shellConfig)
	if err != nil {
		log.Printf("Failed to create shell exec: %v", err)
		return "", err
	}

	// Create a connection to the exec
	attachOpts := types.ExecStartCheck{
		Tty: true,
	}

	hijackedResp, err := tc.client.ContainerExecAttach(context.Background(), shellExec.ID, attachOpts)
	if err != nil {
		log.Printf("Failed to attach to exec: %v", err)
		return "", err
	}

	// Generate a unique shell ID
	shellID := fmt.Sprintf("shell-%s", uuid.New().String())

	// Create log file for the shell
	logFile := tc.logger.CreateSessionLog(session.ContainerID, userID, shellID, r)

	// Add the new shell to the session
	session.Shells[shellID] = &ShellInfo{
		ExecID:           shellExec.ID,
		HijackedResponse: &hijackedResp,
		TabID:            tabID,
		LogFile:          logFile,
		CurrentCommand:   "",
		Buffer:           "",
		CommandComplete:  false,
	}

	log.Printf("Shell %s created successfully for session %s", shellID, wsID)
	return shellID, nil
}

// setReadDeadline sets a deadline for reading from a connection
func (tc *TTYController) setReadDeadline(conn net.Conn, timeout time.Duration) {
	if conn, ok := conn.(interface{ SetReadDeadline(time.Time) error }); ok {
		conn.SetReadDeadline(time.Now().Add(timeout))
	}
}

// WriteToShell writes data to a specific shell in a session
func (tc *TTYController) WriteToShell(wsID, shellID, data string) error {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	session, ok := tc.sessions[wsID]
	if !ok {
		return fmt.Errorf("session not found: %s", wsID)
	}

	shell, ok := session.Shells[shellID]
	if !ok {
		return fmt.Errorf("shell not found: %s", shellID)
	}

	// Handle command building for logging
	if strings.Contains(data, "\r") || strings.Contains(data, "\n") {
		// Enter key pressed
		shell.PendingCommand = strings.TrimSpace(shell.CurrentCommand)
		shell.CurrentCommand = ""
		shell.OutputBuffer = ""
		shell.CommandStartTime = time.Now()
	} else if strings.Contains(data, "\x7f") || strings.Contains(data, "\x08") {
		// Backspace (DEL or BS)
		if len(shell.CurrentCommand) > 0 {
			shell.CurrentCommand = shell.CurrentCommand[:len(shell.CurrentCommand)-1]
		}
	} else if strings.HasPrefix(data, "\x1b[") {
		// Arrow keys or other escape sequences - don't add to command
	} else {
		shell.CurrentCommand += data
	}

	// Write data to container
	_, err := shell.HijackedResponse.Conn.Write([]byte(data))
	return err
}

// containsPrompt checks if the output contains a terminal prompt
func (tc *TTYController) containsPrompt(text string) bool {
	promptRE := regexp.MustCompile(`termuser@container.*\$|~\$`)
	return promptRE.MatchString(text)
}

// checkShellCommandTimeout checks if a command has timed out
func (tc *TTYController) checkShellCommandTimeout(wsID, shellID string, shell *ShellInfo) {
	if shell.PendingCommand != "" && !shell.CommandStartTime.IsZero() &&
		time.Since(shell.CommandStartTime) > time.Second {
		tc.finalizeShellCommand(wsID, shellID, shell)
	}
}

// finalizeShellCommand logs a command and its output
func (tc *TTYController) finalizeShellCommand(wsID, shellID string, shell *ShellInfo) {
	if shell.PendingCommand != "" {
		tc.logger.LogCommand(
			shell.LogFile,
			shell.PendingCommand,
			shell.OutputBuffer,
		)
		shell.PendingCommand = ""
		shell.OutputBuffer = ""
		shell.CommandStartTime = time.Time{}
	}
}

// ReadFromShell reads data from a specific shell in a session
func (tc *TTYController) ReadFromShell(wsID, shellID string) (string, error) {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	session, ok := tc.sessions[wsID]
	if !ok {
		return "", fmt.Errorf("session not found: %s", wsID)
	}

	shell, ok := session.Shells[shellID]
	if !ok {
		return "", fmt.Errorf("shell not found: %s", shellID)
	}

	buffer := make([]byte, 4096)
	tc.setReadDeadline(shell.HijackedResponse.Conn, 100*time.Millisecond)

	n, err := shell.HijackedResponse.Reader.Read(buffer)
	if err != nil {
		if err == io.EOF {
			return "", fmt.Errorf("connection closed")
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// Check if we need to finalize previous command
			tc.checkShellCommandTimeout(wsID, shellID, shell)
			return "", nil
		}
		return "", err
	}

	if n == 0 {
		tc.checkShellCommandTimeout(wsID, shellID, shell)
		return "", nil
	}

	output := string(buffer[:n])

	// Add output to the buffer for the current command
	if shell.PendingCommand != "" {
		shell.OutputBuffer += output
	}

	// If we are tracking a pending command, check if this output contains a new prompt
	if shell.PendingCommand != "" && tc.containsPrompt(output) {
		tc.finalizeShellCommand(wsID, shellID, shell)
	}

	// Check for timeout even if we received data
	tc.checkShellCommandTimeout(wsID, shellID, shell)

	return output, nil
}

// ResizeShell resizes a specific shell's terminal
func (tc *TTYController) ResizeShell(wsID, shellID string, cols, rows int) error {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	session, ok := tc.sessions[wsID]
	if !ok {
		return fmt.Errorf("session not found: %s", wsID)
	}

	shell, ok := session.Shells[shellID]
	if !ok {
		return fmt.Errorf("shell not found: %s", shellID)
	}

	// Use stty command
	execConfig := types.ExecConfig{
		Cmd:          []string{"stty", "cols", strconv.Itoa(cols), "rows", strconv.Itoa(rows)},
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	}

	exec, err := tc.client.ContainerExecCreate(context.Background(), session.ContainerID, execConfig)
	if err != nil {
		log.Printf("Failed to create stty exec: %v", err)
		return err
	}

	startOptions := types.ExecStartCheck{Tty: true}
	err = tc.client.ContainerExecStart(context.Background(), exec.ID, startOptions)
	if err != nil {
		log.Printf("Failed to start stty exec: %v", err)
		return err
	}

	// Also try to resize the exec instance
	err = tc.client.ContainerExecResize(context.Background(), shell.ExecID, types.ResizeOptions{
		Height: uint(rows),
		Width:  uint(cols),
	})
	if err != nil {
		log.Printf("Exec resize failed (this is normal for some container runtimes): %v", err)
	}

	return nil
}

// CloseShell closes a specific shell in a session
func (tc *TTYController) CloseShell(wsID, shellID string) error {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	session, ok := tc.sessions[wsID]
	if !ok {
		log.Printf("Session %s not found when closing shell %s", wsID, shellID)
		return nil
	}

	shell, ok := session.Shells[shellID]
	if !ok {
		log.Printf("Shell %s not found in session %s", shellID, wsID)
		return nil
	}

	log.Printf("Closing shell %s in session %s", shellID, wsID)

	// First try to terminate the shell gracefully
	if shell.HijackedResponse != nil {
		// Send Ctrl+D (EOF) followed by exit command
		shell.HijackedResponse.Conn.Write([]byte{0x04})
		time.Sleep(100 * time.Millisecond)
		shell.HijackedResponse.Conn.Write([]byte("exit\r\n"))
		time.Sleep(100 * time.Millisecond)

		// Close the connection
		shell.HijackedResponse.Close()
		log.Printf("Closed connection for shell %s", shellID)
	}

	// If exec_id exists, try to forcefully terminate
	if shell.ExecID != "" {
		// Try to execute a ps command to find and kill the shell process
		execConfig := types.ExecConfig{
			Cmd:          []string{"pkill", "-P", "$(ps -o pid= -p $(ps -o ppid= -p $$))"},
			AttachStdout: true,
			AttachStderr: true,
		}

		exec, err := tc.client.ContainerExecCreate(context.Background(), session.ContainerID, execConfig)
		if err != nil {
			log.Printf("Error creating kill exec: %v", err)
		} else {
			err = tc.client.ContainerExecStart(context.Background(), exec.ID, types.ExecStartCheck{})
			if err != nil {
				log.Printf("Error starting kill exec: %v", err)
			}
		}
	}

	// Remove shell from session
	delete(session.Shells, shellID)
	log.Printf("Shell %s closed successfully", shellID)
	return nil
}

// CleanupSession cleans up a session and its container
func (tc *TTYController) CleanupSession(wsID string) {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	session, ok := tc.sessions[wsID]
	if !ok {
		return
	}

	userID := session.UserID
	containerID := session.ContainerID[:12]

	log.Printf("Cleaning up session %s for user %s (container: %s)", wsID, userID, containerID)

	// Close all shell connections
	for shellID, shell := range session.Shells {
		if shell.HijackedResponse != nil {
			shell.HijackedResponse.Close()
			log.Printf("Closed connection for shell %s", shellID)
		}
	}

	// Stop and remove container
	err := tc.client.ContainerStop(context.Background(), session.ContainerID, pointer(time.Second))
	if err != nil {
		log.Printf("Error stopping container %s: %v", containerID, err)
	}

	err = tc.client.ContainerRemove(context.Background(), session.ContainerID, types.ContainerRemoveOptions{
		Force: true,
	})
	if err != nil {
		log.Printf("Error removing container %s: %v", containerID, err)
	}

	// Clean up user session mapping
	if _, ok := tc.userSessions[userID]; ok {
		delete(tc.userSessions, userID)
		log.Printf("Removed user session mapping for %s", userID)
	}

	// Remove from sessions map
	delete(tc.sessions, wsID)
	log.Printf("Session %s cleanup completed", wsID)
}

// CleanupAllContainers cleans up all active sessions
func (tc *TTYController) CleanupAllContainers() {
	log.Printf("Initiating shutdown and container cleanup...")

	tc.lock.Lock()
	sessionIDs := make([]string, 0, len(tc.sessions))
	for wsID := range tc.sessions {
		sessionIDs = append(sessionIDs, wsID)
	}
	tc.lock.Unlock()

	log.Printf("Cleaning up %d active sessions", len(sessionIDs))
	for _, wsID := range sessionIDs {
		tc.CleanupSession(wsID)
	}

	log.Printf("All sessions cleaned up successfully")
}

// WebSocket connection upgrader
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins
	},
}

// WebSocketHandler handles terminal WebSocket connections
type WebSocketHandler struct {
	ttyController *TTYController
}

// NewWebSocketHandler creates a new WebSocketHandler
func NewWebSocketHandler(ttyController *TTYController) *WebSocketHandler {
	return &WebSocketHandler{
		ttyController: ttyController,
	}
}

// HandleTerminal handles terminal WebSocket connections
func (wsh *WebSocketHandler) HandleTerminal(c *gin.Context) {
	// Upgrade to WebSocket connection
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade to WebSocket: %v", err)
		return
	}
	defer conn.Close()

	// Generate a unique session ID
	wsID := uuid.New().String()
	userID := wsID // Using wsID as userID for now

	// Send session ID to client
	err = conn.WriteJSON(map[string]interface{}{
		"type":    "session_id",
		"id":      wsID,
		"user_id": userID,
	})
	if err != nil {
		log.Printf("Failed to send session ID: %v", err)
		return
	}

	// Create a channel for inbound messages
	messages := make(chan map[string]interface{})

	// Start goroutine to read messages from client
	go func() {
		defer close(messages)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Error reading message: %v", err)
				return
			}

			var data map[string]interface{}
			err = json.Unmarshal(msg, &data)
			if err != nil {
				log.Printf("Error parsing message: %v", err)
				continue
			}

			messages <- data
		}
	}()

	// Process messages
	for msg := range messages {
		switch msg["type"] {
		case "start_session":
			containerID, shellID, err := wsh.ttyController.CreateSessionWithShells(wsID, userID, c.Request)
			if err != nil {
				conn.WriteJSON(map[string]interface{}{
					"type":  "error",
					"error": err.Error(),
				})
				return
			}

			// Send container ready event
			conn.WriteJSON(map[string]interface{}{
				"type":        "container_ready",
				"containerId": containerID,
				"shellId":     shellID,
			})

			// Start goroutine to read shell output
			go wsh.readShellOutput(conn, wsID, shellID)

		case "shell_input":
			shellID := msg["shellId"].(string)
			input := msg["input"].(string)
			err := wsh.ttyController.WriteToShell(wsID, shellID, input)
			if err != nil {
				conn.WriteJSON(map[string]interface{}{
					"type":  "error",
					"error": err.Error(),
				})
			}

		case "resize_shell":
			shellID := msg["shellId"].(string)
			cols := int(msg["cols"].(float64))
			rows := int(msg["rows"].(float64))

			err := wsh.ttyController.ResizeShell(wsID, shellID, cols, rows)
			if err != nil {
				conn.WriteJSON(map[string]interface{}{
					"type":  "error",
					"error": err.Error(),
				})
			}

		case "create_shell":
			tabID := msg["tabId"].(string)
			shellID, err := wsh.ttyController.CreateShell(wsID, tabID, userID, c.Request)
			if err != nil {
				conn.WriteJSON(map[string]interface{}{
					"type":  "error",
					"error": err.Error(),
				})
				continue
			}

			// Send shell created event
			conn.WriteJSON(map[string]interface{}{
				"type":    "shell_created",
				"tabId":   tabID,
				"shellId": shellID,
			})

			// Start goroutine to read shell output
			go wsh.readShellOutput(conn, wsID, shellID)

		case "close_shell":
			shellID := msg["shellId"].(string)
			err := wsh.ttyController.CloseShell(wsID, shellID)
			if err != nil {
				conn.WriteJSON(map[string]interface{}{
					"type":  "error",
					"error": err.Error(),
				})
			}
		}
	}

	// Cleanup when WebSocket closes
	wsh.ttyController.CleanupSession(wsID)
}

// readShellOutput continuously reads output from a shell and sends it to the WebSocket
func (wsh *WebSocketHandler) readShellOutput(conn *websocket.Conn, wsID, shellID string) {
	for {
		output, err := wsh.ttyController.ReadFromShell(wsID, shellID)
		if err != nil {
			log.Printf("Error reading from shell %s: %v", shellID, err)
			return
		}

		if output != "" {
			err = conn.WriteJSON(map[string]interface{}{
				"type":    "shell_output",
				"shellId": shellID,
				"output":  output,
			})
			if err != nil {
				log.Printf("Error sending output: %v", err)
				return
			}
		}

		// Small sleep to prevent CPU spinning
		time.Sleep(50 * time.Millisecond)
	}
}

func main() {
	// Setup logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting web terminal server...")

	// Create TTY controller
	ttyController, err := NewTTYController()
	if err != nil {
		log.Fatalf("Failed to create TTY controller: %v", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Create Gin router
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	// Enable CORS
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"}
	router.Use(cors.New(config))

	// Create WebSocket handler
	wsHandler := NewWebSocketHandler(ttyController)

	// Static files
	router.Static("/static", "./static")

	// Routes
	router.GET("/", func(c *gin.Context) {
		c.File("templates/terminal.html")
	})
	router.GET("/ws", wsHandler.HandleTerminal)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "5000"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Server starting on port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()
	log.Println("Shutdown signal received")

	// Cleanup all containers
	ttyController.CleanupAllContainers()

	// Create a deadline for server shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown the server
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	log.Println("Server shutdown completed")
}
