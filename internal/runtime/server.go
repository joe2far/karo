package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MCPServer implements an MCP server over stdio for the agent-runtime-mcp sidecar.
type MCPServer struct {
	kubeClient client.Client
	tools      *ToolHandler
	namespace  string
	agentInst  string
	agentSpec  string
	mailbox    string
}

// NewMCPServer creates a new MCP server.
func NewMCPServer(kubeClient client.Client) *MCPServer {
	s := &MCPServer{
		kubeClient: kubeClient,
		namespace:  os.Getenv("KARO_NAMESPACE"),
		agentInst:  os.Getenv("KARO_AGENT_INSTANCE"),
		agentSpec:  os.Getenv("KARO_AGENT_SPEC"),
		mailbox:    os.Getenv("KARO_MAILBOX"),
	}
	s.tools = NewToolHandler(kubeClient, s.namespace, s.agentInst, s.agentSpec, s.mailbox)
	return s
}

// MCPRequest represents an MCP JSON-RPC request.
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPResponse represents an MCP JSON-RPC response.
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPError represents an MCP error.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve runs the MCP server on stdio.
func (s *MCPServer) Serve(ctx context.Context, reader io.Reader, writer io.Writer) error {
	decoder := json.NewDecoder(reader)
	encoder := json.NewEncoder(writer)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var req MCPRequest
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("failed to decode request: %w", err)
		}

		resp := s.handleRequest(ctx, &req)
		if err := encoder.Encode(resp); err != nil {
			return fmt.Errorf("failed to encode response: %w", err)
		}
	}
}

func (s *MCPServer) handleRequest(ctx context.Context, req *MCPRequest) *MCPResponse {
	switch req.Method {
	case "initialize":
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "agent-runtime-mcp",
					"version": "0.4.0-alpha",
				},
			},
		}
	case "tools/list":
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": s.tools.ListTools(),
			},
		}
	case "tools/call":
		return s.handleToolCall(ctx, req)
	default:
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &MCPError{Code: -32601, Message: "Method not found: " + req.Method},
		}
	}
}

func (s *MCPServer) handleToolCall(ctx context.Context, req *MCPRequest) *MCPResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &MCPError{Code: -32602, Message: "Invalid params"},
		}
	}

	result, err := s.tools.CallTool(ctx, params.Name, params.Arguments)
	if err != nil {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &MCPError{Code: -32000, Message: err.Error()},
		}
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": string(result)},
			},
		},
	}
}
