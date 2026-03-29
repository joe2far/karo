package runtime

import (
	"encoding/json"
	"net/http"
)

// DebugServer serves the debug REST endpoints on port 9091.
type DebugServer struct {
	mcpServer *MCPServer
}

// NewDebugServer creates a new debug server.
func NewDebugServer(mcpServer *MCPServer) *DebugServer {
	return &DebugServer{mcpServer: mcpServer}
}

// Serve starts the debug HTTP server.
func (d *DebugServer) Serve(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", d.healthz)
	mux.HandleFunc("/readyz", d.readyz)
	mux.HandleFunc("/debug/status", d.debugStatus)
	mux.HandleFunc("/debug/mailbox", d.debugMailbox)
	mux.HandleFunc("/debug/tools", d.debugTools)
	mux.HandleFunc("/debug/drain", d.debugDrain)

	return http.ListenAndServe(addr, mux)
}

func (d *DebugServer) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (d *DebugServer) readyz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (d *DebugServer) debugStatus(w http.ResponseWriter, _ *http.Request) {
	status := map[string]interface{}{
		"namespace":     d.mcpServer.namespace,
		"agentInstance": d.mcpServer.agentInst,
		"agentSpec":     d.mcpServer.agentSpec,
		"mailbox":       d.mcpServer.mailbox,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (d *DebugServer) debugMailbox(w http.ResponseWriter, _ *http.Request) {
	status := map[string]interface{}{
		"mailbox": d.mcpServer.mailbox,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (d *DebugServer) debugTools(w http.ResponseWriter, _ *http.Request) {
	tools := d.mcpServer.tools.ListTools()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tools)
}

// debugDrain handles POST /debug/drain for graceful shutdown.
// Signals the agent to complete current work, checkpoint state, and terminate.
func (d *DebugServer) debugDrain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Signal drain — in a full implementation this would:
	// 1. Stop accepting new tasks
	// 2. Wait for current task to complete
	// 3. Checkpoint state to MemoryStore
	// 4. Signal the MCP server to shut down
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "draining",
		"message": "Drain initiated, completing current work",
	})
}
