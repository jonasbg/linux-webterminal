package logger

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jonasbg/linux-terminal/m/v2/utils"
)

// CommandTracker tracks commands and their output
type CommandTracker struct {
	CurrentCommand   string
	PendingCommand   string
	OutputBuffer     string
	CommandStartTime time.Time
	LogFile          string
}

// TTYLogger handles logging for terminal sessions.
type TTYLogger struct {
	enabled         bool
	logDir          string
	commandTrackers map[string]*CommandTracker
	promptPatterns  []*regexp.Regexp // Store pre-compiled patterns
	mu              sync.Mutex
}

// NewTTYLogger initializes a new TTYLogger.
func NewTTYLogger() *TTYLogger {
	// Default to enabled unless explicitly disabled
	enabled := os.Getenv("TTY_LOGGING_ENABLED") == "true"
	logDir := os.Getenv("TTY_LOG_DIR")
	if logDir == "" {
		logDir = "./logs"
	}

	if enabled {
		err := os.MkdirAll(logDir, 0755)
		if err != nil {
			log.Printf("Warning: Failed to create log directory %s: %v", logDir, err)
		}
		log.Printf("TTY logging enabled. Logs will be saved to: %s", logDir)
	} else {
		log.Printf("TTY logging is disabled")
	}

	// Pre-compile prompt patterns - defined in ONLY ONE place
	promptPatterns := []*regexp.Regexp{
		regexp.MustCompile(`termuser@container.*\$`),
		regexp.MustCompile(`~\$`),
		regexp.MustCompile(`\$ $`),
		regexp.MustCompile(`> $`),
		regexp.MustCompile(`❯\s*`), // Your custom prompt
	}

	return &TTYLogger{
		enabled:         enabled,
		logDir:          logDir,
		commandTrackers: make(map[string]*CommandTracker),
		promptPatterns:  promptPatterns,
	}
}

// CreateSessionLog creates a log file for a session.
func (l *TTYLogger) CreateSessionLog(containerID, userID, shellID string, r *http.Request) string {
	if !l.enabled {
		return ""
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := filepath.Join(l.logDir, fmt.Sprintf("%s-%s-%s.log", timestamp, containerID[:12], shellID))
	f, err := os.Create(filename)
	if err != nil {
		log.Printf("Error creating log file %s: %v", filename, err)
		return ""
	}
	defer f.Close()

	metadata := "Not available"
	if r != nil {
		meta := utils.GetRequestMetadata(r)
		metadata = fmt.Sprintf("%s (via %s)", meta["ip_address"], meta["ip_source"])
	}

	fmt.Fprintf(f, "Session Start: %s\nContainer ID: %s\nUser ID: %s\nShell ID: %s\nOrigin IP: %s\nUser Agent: %s\n\n=== Command History ===\n\n",
		timestamp, containerID, userID, shellID, metadata, utils.GetUserAgent(r))

	// Create and store a command tracker for this shell
	l.mu.Lock()
	defer l.mu.Unlock()
	l.commandTrackers[shellID] = &CommandTracker{
		LogFile: filename,
	}

	log.Printf("Created log file %s for shell %s", filename, shellID)
	return filename
}

// HandleInput processes terminal input for command tracking
func (l *TTYLogger) HandleInput(shellID, input string) {
	if !l.enabled {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	tracker, exists := l.commandTrackers[shellID]
	if !exists {
		return
	}

	// Command tracking for logging
	if containsEnter(input) {
		if tracker.CurrentCommand != "" {
			tracker.PendingCommand = tracker.CurrentCommand
			tracker.CurrentCommand = ""
			tracker.OutputBuffer = ""
			tracker.CommandStartTime = time.Now()
		}
	} else if containsBackspace(input) {
		if len(tracker.CurrentCommand) > 0 {
			tracker.CurrentCommand = tracker.CurrentCommand[:len(tracker.CurrentCommand)-1]
		}
	} else if !isEscapeSequence(input) {
		tracker.CurrentCommand += input
	}
}

// HandleOutput processes terminal output for command tracking and logging
func (l *TTYLogger) HandleOutput(shellID, output string) {
	if !l.enabled {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	tracker, exists := l.commandTrackers[shellID]
	if !exists {
		return
	}

	// Append output to buffer
	tracker.OutputBuffer += output

	// Check if this output contains a prompt, indicating command completion
	if tracker.PendingCommand != "" {
		if l.containsPrompt(output) || l.containsPrompt(tracker.OutputBuffer) {
			l.finalizeCommand(shellID)
		} else if time.Since(tracker.CommandStartTime) > 5*time.Second {
			// Time-based fallback
			log.Printf("Command timeout for shell %s, finalizing: %q",
				shellID, tracker.PendingCommand)
			l.finalizeCommand(shellID)
		}
	}
}

// CleanupShell removes tracking for a closed shell
func (l *TTYLogger) CleanupShell(shellID string) {
	if !l.enabled {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	tracker, exists := l.commandTrackers[shellID]
	if !exists {
		return
	}

	// Finalize any pending command
	if tracker.PendingCommand != "" {
		l.finalizeCommand(shellID)
	}

	delete(l.commandTrackers, shellID)
	log.Printf("Cleaned up command tracker for shell %s", shellID)
}

// containsPrompt checks if text contains a shell prompt
// Uses the pre-compiled patterns from the struct
func (l *TTYLogger) containsPrompt(text string) bool {
	for _, pattern := range l.promptPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// Internal method to finalize a command - caller must hold mutex
func (l *TTYLogger) finalizeCommand(shellID string) {
	tracker := l.commandTrackers[shellID]
	if tracker.PendingCommand == "" {
		return
	}

	l.LogCommand(tracker.LogFile, tracker.PendingCommand, tracker.OutputBuffer)

	tracker.PendingCommand = ""
	tracker.OutputBuffer = ""
	tracker.CommandStartTime = time.Time{}
}

// LogCommand logs a command and its output.
func (l *TTYLogger) LogCommand(filename, command, output string) {
	if !l.enabled || filename == "" {
		return
	}

	f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error opening log file %s: %v", filename, err)
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("15:04:05")
	cleanedOutput := l.cleanTerminalOutput(output, command)
	fmt.Fprintf(f, "[%s] > %s\n%s\n\n", timestamp, command, cleanedOutput)
}

func (l *TTYLogger) cleanTerminalOutput(text, command string) string {
	// Remove ANSI escape sequences
	ansiEscape := regexp.MustCompile(`\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`)
	text = ansiEscape.ReplaceAllString(text, "")

	// Remove specific control sequences
	replacements := map[string]string{
		"\x1b[6n": "", "\x1b[J": "", "\x1b[K": "", "\x07": "",
	}
	for old, new := range replacements {
		text = strings.ReplaceAll(text, old, new)
	}
	text = strings.ReplaceAll(text, "\r", "\n")

	// Process lines
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		// Skip prompt lines
		if l.containsPrompt(line) {
			continue
		}

		// Handle backspaces
		for strings.Contains(line, "\b") {
			line = regexp.MustCompile(`.\b`).ReplaceAllString(line, "")
		}
		lines = append(lines, line)
	}

	text = strings.Join(lines, "\n")

	// Remove the command itself from the beginning of output
	if command != "" {
		text = regexp.MustCompile(fmt.Sprintf("^%s", regexp.QuoteMeta(command))).ReplaceAllString(text, "")
	}

	text = strings.TrimSpace(text)
	text = regexp.MustCompile(`\n\s*\n\s*\n+`).ReplaceAllString(text, "\n\n")
	return text
}

// Helper functions
func containsEnter(data string) bool {
	return data == "\r" || data == "\n"
}

func containsBackspace(data string) bool {
	return data == "\x7f" || data == "\x08"
}

func isEscapeSequence(data string) bool {
	return len(data) > 0 && data[0] == '\x1b'
}
