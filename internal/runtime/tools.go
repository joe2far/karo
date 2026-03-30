package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ToolHandler implements the 8 MCP tools for agent-runtime-mcp.
type ToolHandler struct {
	client    client.Client
	namespace string
	agentInst string
	agentSpec string
	mailbox   string
}

// NewToolHandler creates a new tool handler.
func NewToolHandler(c client.Client, namespace, agentInst, agentSpec, mailbox string) *ToolHandler {
	return &ToolHandler{
		client:    c,
		namespace: namespace,
		agentInst: agentInst,
		agentSpec: agentSpec,
		mailbox:   mailbox,
	}
}

// ToolDefinition represents an MCP tool definition.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

// ListTools returns all available MCP tool definitions.
func (h *ToolHandler) ListTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "karo_poll_mailbox",
			Description: "Get pending messages from your mailbox.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"messageTypes": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"limit":        map[string]interface{}{"type": "integer"},
				},
			},
		},
		{
			Name:        "karo_ack_message",
			Description: "Acknowledge a message as processed.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"messageId": map[string]interface{}{"type": "string"}},
				"required":   []string{"messageId"},
			},
		},
		{
			Name:        "karo_complete_task",
			Description: "Mark a task as complete and submit the result.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"taskGraphName":    map[string]interface{}{"type": "string"},
					"taskId":           map[string]interface{}{"type": "string"},
					"resultArtifactRef": map[string]interface{}{"type": "string"},
					"notes":            map[string]interface{}{"type": "string"},
				},
				"required": []string{"taskGraphName", "taskId", "resultArtifactRef"},
			},
		},
		{
			Name:        "karo_fail_task",
			Description: "Report that you cannot complete a task.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"taskGraphName": map[string]interface{}{"type": "string"},
					"taskId":        map[string]interface{}{"type": "string"},
					"reason":        map[string]interface{}{"type": "string"},
				},
				"required": []string{"taskGraphName", "taskId", "reason"},
			},
		},
		{
			Name:        "karo_add_task",
			Description: "Add a new task to a TaskGraph.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"taskGraphName": map[string]interface{}{"type": "string"},
					"task":          map[string]interface{}{"type": "object"},
				},
				"required": []string{"taskGraphName", "task"},
			},
		},
		{
			Name:        "karo_query_memory",
			Description: "Search your memory store for relevant memories.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":      map[string]interface{}{"type": "string"},
					"categories": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"limit":      map[string]interface{}{"type": "integer"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "karo_store_memory",
			Description: "Store a memory in your memory store.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content":  map[string]interface{}{"type": "string"},
					"category": map[string]interface{}{"type": "string"},
					"metadata": map[string]interface{}{"type": "object"},
				},
				"required": []string{"content", "category"},
			},
		},
		{
			Name:        "karo_report_status",
			Description: "Report your runtime status to the operator.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"contextTokensUsed": map[string]interface{}{"type": "integer"},
					"status":            map[string]interface{}{"type": "string", "enum": []string{"active", "idle", "checkpoint-requested"}},
					"notes":             map[string]interface{}{"type": "string"},
				},
				"required": []string{"contextTokensUsed", "status"},
			},
		},
	}
}

// CallTool dispatches a tool call to the appropriate handler.
func (h *ToolHandler) CallTool(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	switch name {
	case "karo_poll_mailbox":
		return h.pollMailbox(ctx, args)
	case "karo_ack_message":
		return h.ackMessage(ctx, args)
	case "karo_complete_task":
		return h.completeTask(ctx, args)
	case "karo_fail_task":
		return h.failTask(ctx, args)
	case "karo_add_task":
		return h.addTask(ctx, args)
	case "karo_query_memory":
		return h.queryMemory(ctx, args)
	case "karo_store_memory":
		return h.storeMemory(ctx, args)
	case "karo_report_status":
		return h.reportStatus(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (h *ToolHandler) pollMailbox(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		MessageTypes []string `json:"messageTypes"`
		Limit        int      `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, err
	}
	if params.Limit == 0 {
		params.Limit = 10
	}

	var mailbox karov1alpha1.AgentMailbox
	if err := h.client.Get(ctx, types.NamespacedName{
		Namespace: h.namespace,
		Name:      h.mailbox,
	}, &mailbox); err != nil {
		return nil, err
	}

	var messages []karov1alpha1.MailboxMessage
	for _, msg := range mailbox.Status.PendingMessages {
		if len(params.MessageTypes) > 0 {
			found := false
			for _, mt := range params.MessageTypes {
				if string(msg.MessageType) == mt {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		messages = append(messages, msg)
		if len(messages) >= params.Limit {
			break
		}
	}

	return json.Marshal(map[string]interface{}{
		"messages":     messages,
		"pendingCount": mailbox.Status.PendingCount,
	})
}

func (h *ToolHandler) ackMessage(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, err
	}

	var mailbox karov1alpha1.AgentMailbox
	if err := h.client.Get(ctx, types.NamespacedName{
		Namespace: h.namespace,
		Name:      h.mailbox,
	}, &mailbox); err != nil {
		return nil, err
	}

	filtered := make([]karov1alpha1.MailboxMessage, 0, len(mailbox.Status.PendingMessages))
	found := false
	for _, msg := range mailbox.Status.PendingMessages {
		if msg.MessageID == params.MessageID {
			found = true
			mailbox.Status.TotalProcessed++
			continue
		}
		filtered = append(filtered, msg)
	}
	if !found {
		return nil, fmt.Errorf("message %s not found", params.MessageID)
	}

	mailbox.Status.PendingMessages = filtered
	mailbox.Status.PendingCount = int32(len(filtered))
	if err := h.client.Status().Update(ctx, &mailbox); err != nil {
		return nil, err
	}

	return json.Marshal(map[string]interface{}{"acknowledged": true})
}

func (h *ToolHandler) completeTask(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		TaskGraphName    string `json:"taskGraphName"`
		TaskID           string `json:"taskId"`
		ResultArtifactRef string `json:"resultArtifactRef"`
		Notes            string `json:"notes"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, err
	}

	var tg karov1alpha1.TaskGraph
	if err := h.client.Get(ctx, types.NamespacedName{
		Namespace: h.namespace,
		Name:      params.TaskGraphName,
	}, &tg); err != nil {
		return nil, err
	}

	ts, exists := tg.Status.TaskStatuses[params.TaskID]
	if !exists {
		return nil, fmt.Errorf("task %s not found in TaskGraph %s", params.TaskID, params.TaskGraphName)
	}

	ts.ResultArtifactRef = params.ResultArtifactRef

	// Check if task has eval gate
	var hasEvalGate bool
	for _, task := range tg.Spec.Tasks {
		if task.ID == params.TaskID && task.EvalGate != nil {
			hasEvalGate = true
			break
		}
	}

	if hasEvalGate {
		ts.Phase = karov1alpha1.TaskPhaseEvalPending
	} else {
		ts.Phase = karov1alpha1.TaskPhaseClosed
		now := metav1.Now()
		ts.CompletedAt = &now
	}
	tg.Status.TaskStatuses[params.TaskID] = ts

	if err := h.client.Status().Update(ctx, &tg); err != nil {
		return nil, err
	}

	return json.Marshal(map[string]interface{}{
		"accepted": true,
		"newPhase": string(ts.Phase),
		"message":  "Task completion submitted",
	})
}

func (h *ToolHandler) failTask(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		TaskGraphName string `json:"taskGraphName"`
		TaskID        string `json:"taskId"`
		Reason        string `json:"reason"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, err
	}

	var tg karov1alpha1.TaskGraph
	if err := h.client.Get(ctx, types.NamespacedName{
		Namespace: h.namespace,
		Name:      params.TaskGraphName,
	}, &tg); err != nil {
		return nil, err
	}

	ts, exists := tg.Status.TaskStatuses[params.TaskID]
	if !exists {
		return nil, fmt.Errorf("task %s not found", params.TaskID)
	}

	ts.Phase = karov1alpha1.TaskPhaseFailed
	ts.FailureNotes = params.Reason
	tg.Status.TaskStatuses[params.TaskID] = ts

	if err := h.client.Status().Update(ctx, &tg); err != nil {
		return nil, err
	}

	return json.Marshal(map[string]interface{}{"accepted": true})
}

func (h *ToolHandler) addTask(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		TaskGraphName string `json:"taskGraphName"`
		Task          struct {
			ID                 string   `json:"id"`
			Title              string   `json:"title"`
			Type               string   `json:"type"`
			Description        string   `json:"description"`
			Deps               []string `json:"deps"`
			Priority           string   `json:"priority"`
			TimeoutMinutes     *int32   `json:"timeoutMinutes"`
			AcceptanceCriteria []string `json:"acceptanceCriteria"`
		} `json:"task"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, err
	}

	var tg karov1alpha1.TaskGraph
	if err := h.client.Get(ctx, types.NamespacedName{
		Namespace: h.namespace,
		Name:      params.TaskGraphName,
	}, &tg); err != nil {
		return nil, err
	}

	if !tg.Spec.DispatchPolicy.AllowAgentMutation {
		return nil, fmt.Errorf("TaskGraph %s does not allow agent mutation", params.TaskGraphName)
	}

	newTask := karov1alpha1.Task{
		ID:                 params.Task.ID,
		Title:              params.Task.Title,
		Type:               karov1alpha1.TaskType(params.Task.Type),
		Description:        params.Task.Description,
		Deps:               params.Task.Deps,
		Priority:           karov1alpha1.TaskPriority(params.Task.Priority),
		AddedBy:            h.agentSpec,
		AddedAt:            metav1.Now(),
		TimeoutMinutes:     params.Task.TimeoutMinutes,
		AcceptanceCriteria: params.Task.AcceptanceCriteria,
	}

	tg.Spec.Tasks = append(tg.Spec.Tasks, newTask)
	if err := h.client.Update(ctx, &tg); err != nil {
		return nil, err
	}

	return json.Marshal(map[string]interface{}{
		"accepted": true,
		"taskId":   params.Task.ID,
		"message":  "Task added successfully",
	})
}

func (h *ToolHandler) queryMemory(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		Query      string   `json:"query"`
		Categories []string `json:"categories"`
		Limit      int      `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, err
	}

	// Memory query requires backend integration (mem0, redis, pgvector)
	// Stub returns empty results for now
	return json.Marshal(map[string]interface{}{
		"memories": []interface{}{},
	})
}

func (h *ToolHandler) storeMemory(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		Content  string                 `json:"content"`
		Category string                 `json:"category"`
		Metadata map[string]interface{} `json:"metadata"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, err
	}

	// Memory store requires backend integration
	// Stub returns success
	memoryID := fmt.Sprintf("mem-%d", time.Now().UnixNano())
	return json.Marshal(map[string]interface{}{
		"memoryId": memoryID,
		"stored":   true,
	})
}

func (h *ToolHandler) reportStatus(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var params struct {
		ContextTokensUsed int64  `json:"contextTokensUsed"`
		Status            string `json:"status"`
		Notes             string `json:"notes"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, err
	}

	var instance karov1alpha1.AgentInstance
	if err := h.client.Get(ctx, types.NamespacedName{
		Namespace: h.namespace,
		Name:      h.agentInst,
	}, &instance); err != nil {
		return nil, err
	}

	instance.Status.ContextTokensUsed = params.ContextTokensUsed
	now := metav1.Now()
	instance.Status.LastActiveAt = &now

	if err := h.client.Status().Update(ctx, &instance); err != nil {
		return nil, err
	}

	return json.Marshal(map[string]interface{}{"accepted": true})
}
