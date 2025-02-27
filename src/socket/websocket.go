package socket

import (
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jonasbg/linux-terminal/m/v2/ttycontroller"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Adjust CORS policy as needed
	},
}

func HandleWebSocket(w http.ResponseWriter, r *http.Request, ttyCtrl *ttycontroller.TTYController) {
	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Generate a unique session ID
	sessionID := uuid.New().String()
	userID := r.RemoteAddr // Using remote address as userID for tracking

	// Send session ID to client
	err = conn.WriteJSON(map[string]interface{}{
		"type": "session_id",
		"data": map[string]string{
			"id":      sessionID,
			"user_id": userID,
		},
	})
	if err != nil {
		log.Printf("Failed to send session ID: %v", err)
		return
	}

	// Set up cleanup when the connection is closed
	defer func() {
		log.Printf("Client disconnected (user: %s)", userID)
		ttyCtrl.CleanupSession(sessionID)
	}()

	// Handle WebSocket messages
	for {
		var msg map[string]interface{}
		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}

		msgType, ok := msg["type"].(string)
		if !ok {
			log.Printf("Message missing 'type' field: %v", msg)
			continue
		}

		switch msgType {
		case "start_session":
			data, ok := msg["data"].(map[string]interface{})
			if !ok {
				log.Printf("Invalid data format for start_session: %v", msg)
				continue
			}

			// For compatibility, accept either user_id or id
			_, userIDOk := data["user_id"].(string)
			_, idOk := data["id"].(string)
			if !userIDOk && !idOk {
				log.Printf("Missing user_id or id in start_session: %v", data)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": "Missing user_id or id",
					},
				})
				continue
			}

			shellID, err := ttyCtrl.CreateSession(sessionID, userID, r)
			if err != nil {
				log.Printf("Error creating session: %v", err)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": err.Error(),
					},
				})
				continue
			}

			// Start a goroutine to read output from the main terminal
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("Recovered from panic in main terminal reader: %v", rec)
					}
				}()
				ttyCtrl.ReadShellOutput(sessionID, shellID, conn)
			}()

			// Send container ready notification
			conn.WriteJSON(map[string]string{
				"type": "container_ready",
			})

		case "input":
			data, ok := msg["data"].(map[string]interface{})
			if !ok {
				log.Printf("Invalid data format for input: %v", msg)
				continue
			}

			// Get the session ID from the message
			wsID, ok := data["id"].(string)
			if !ok {
				log.Printf("Missing id in input: %v", data)
				continue
			}

			input, ok := data["input"].(string)
			if !ok {
				log.Printf("Missing input in input message: %v", data)
				continue
			}

			// Get the actual main shell ID from the session
			var shellID string
			shellID, err = ttyCtrl.GetMainShellID(wsID)
			if err != nil {
				shellID = "main" // Fallback to previous behavior
				log.Printf("No stored main shell ID found, using fallback ID: %s", shellID)
			}

			// Write to the main terminal
			if err := ttyCtrl.WriteToShell(wsID, shellID, input); err != nil {
				log.Printf("Error writing to main shell: %v", err)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": err.Error(),
					},
				})
			}
		// Case handler for "resize" message
		case "resize":
			// Handle resize for the main terminal
			data, ok := msg["data"].(map[string]interface{})
			if !ok {
				log.Printf("Invalid data format for resize: %v", msg)
				continue
			}

			id, ok := data["id"].(string)
			if !ok {
				log.Printf("Missing id in resize: %v", data)
				continue
			}

			cols, colsOk := data["cols"].(float64) // JSON numbers are float64
			rows, rowsOk := data["rows"].(float64)

			if !colsOk || !rowsOk {
				log.Printf("Invalid cols/rows in resize: %v", data)
				continue
			}

			// Get the actual main shell ID from the session
			var shellID string
			shellID, err = ttyCtrl.GetMainShellID(id)
			if err != nil {
				shellID = id // Fallback to previous behavior
				log.Printf("No stored main shell ID found, using session ID as shell ID: %s", shellID)
			}

			// Resize the main terminal
			if err := ttyCtrl.ResizeShell(id, shellID, int(cols), int(rows)); err != nil {
				log.Printf("Error resizing terminal: %v", err)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": err.Error(),
					},
				})
			}

		case "create_shell":
			data, ok := msg["data"].(map[string]interface{})
			if !ok {
				log.Printf("Invalid data format for create_shell: %v", msg)
				continue
			}

			// Support multiple field names for compatibility
			var containerID string
			if cID, ok := data["containerId"].(string); ok {
				containerID = cID
			} else if cID, ok := data["container_id"].(string); ok {
				containerID = cID
			} else {
				log.Printf("Missing containerId in create_shell: %v", data)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": "Missing containerId",
					},
				})
				continue
			}

			// Support multiple field names for tab_id
			var tabID string
			if tID, ok := data["tabId"].(string); ok {
				tabID = tID
			} else if tID, ok := data["tab_id"].(string); ok {
				tabID = tID
			} else {
				log.Printf("Missing tabId/tab_id in create_shell: %v", data)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": "Missing tabId/tab_id",
					},
				})
				continue
			}

			shellID, err := ttyCtrl.CreateShell(containerID, tabID, r)
			if err != nil {
				log.Printf("Error creating shell: %v", err)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": err.Error(),
					},
				})
				continue
			}

			// Send shell created notification
			// Send both tab_id and tabId for compatibility
			conn.WriteJSON(map[string]interface{}{
				"type": "shell_created",
				"data": map[string]string{
					"tab_id":   tabID,
					"tabId":    tabID,
					"shell_id": shellID,
					"shellId":  shellID,
				},
			})

			// Start a goroutine to read output from the shell
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("Recovered from panic in shell reader: %v", rec)
					}
				}()
				ttyCtrl.ReadShellOutput(containerID, shellID, conn)
			}()

		case "shell_input":
			data, ok := msg["data"].(map[string]interface{})
			if !ok {
				log.Printf("Invalid data format for shell_input: %v", msg)
				continue
			}

			// Support multiple field names for compatibility
			var containerID string
			if cID, ok := data["containerId"].(string); ok {
				containerID = cID
			} else if cID, ok := data["container_id"].(string); ok {
				containerID = cID
			} else {
				log.Printf("Missing containerId in shell_input: %v", data)
				continue
			}

			// Support multiple field names for shellId
			var shellID string
			if sID, ok := data["shellId"].(string); ok {
				shellID = sID
			} else if sID, ok := data["shell_id"].(string); ok {
				shellID = sID
			} else {
				log.Printf("Missing shellId in shell_input: %v", data)
				continue
			}

			input, ok := data["input"].(string)
			if !ok {
				log.Printf("Missing input in shell_input: %v", data)
				continue
			}

			if err := ttyCtrl.WriteToShell(containerID, shellID, input); err != nil {
				log.Printf("Error writing to shell: %v", err)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": err.Error(),
					},
				})
			}

		case "resize_shell":
			data, ok := msg["data"].(map[string]interface{})
			if !ok {
				log.Printf("Invalid data format for resize_shell: %v", msg)
				continue
			}

			// Support multiple field names for compatibility
			var containerID string
			if cID, ok := data["containerId"].(string); ok {
				containerID = cID
			} else if cID, ok := data["container_id"].(string); ok {
				containerID = cID
			} else {
				log.Printf("Missing containerId in resize_shell: %v", data)
				continue
			}

			// Support multiple field names for shellId
			var shellID string
			if sID, ok := data["shellId"].(string); ok {
				shellID = sID
			} else if sID, ok := data["shell_id"].(string); ok {
				shellID = sID
			} else {
				log.Printf("Missing shellId in resize_shell: %v", data)
				continue
			}

			cols, colsOk := data["cols"].(float64) // JSON numbers are float64
			rows, rowsOk := data["rows"].(float64)

			if !colsOk || !rowsOk {
				log.Printf("Invalid cols/rows in resize_shell: %v", data)
				continue
			}

			if err := ttyCtrl.ResizeShell(containerID, shellID, int(cols), int(rows)); err != nil {
				log.Printf("Error resizing shell: %v", err)
				conn.WriteJSON(map[string]interface{}{
					"type": "error",
					"data": map[string]string{
						"error": err.Error(),
					},
				})
			}

		case "close_shell":
			data, ok := msg["data"].(map[string]interface{})
			if !ok {
				log.Printf("Invalid data format for close_shell: %v", msg)
				continue
			}

			// Support multiple field names for compatibility
			var containerID string
			if cID, ok := data["containerId"].(string); ok {
				containerID = cID
			} else if cID, ok := data["container_id"].(string); ok {
				containerID = cID
			} else {
				log.Printf("Missing containerId in close_shell: %v", data)
				continue
			}

			// Support multiple field names for shellId
			var shellID string
			if sID, ok := data["shellId"].(string); ok {
				shellID = sID
			} else if sID, ok := data["shell_id"].(string); ok {
				shellID = sID
			} else {
				log.Printf("Missing shellId in close_shell: %v", data)
				continue
			}

			ttyCtrl.CloseShell(containerID, shellID)

		case "cleanup":
			data, ok := msg["data"].(map[string]interface{})
			if !ok {
				log.Printf("Invalid data format for cleanup: %v", msg)
				continue
			}

			id, ok := data["id"].(string)
			if !ok {
				log.Printf("Missing id in cleanup: %v", data)
				continue
			}

			ttyCtrl.CleanupSession(id)

		default:
			log.Printf("Unknown message type: %s", msgType)
		}
	}
}
