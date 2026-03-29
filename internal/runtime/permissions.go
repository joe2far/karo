package runtime

// Permission constants for agent-runtime-mcp tools.
const (
	PermissionMailboxRead  = "mailbox-read"
	PermissionMailboxAck   = "mailbox-ack"
	PermissionTaskComplete = "task-complete"
	PermissionTaskFail     = "task-fail"
	PermissionTaskAdd      = "task-add"
	PermissionMemoryRead   = "memory-read"
	PermissionMemoryWrite  = "memory-write"
	PermissionStatusReport = "status-report"
)

// ToolPermissions maps tool names to required permissions.
var ToolPermissions = map[string]string{
	"karo_poll_mailbox":  PermissionMailboxRead,
	"karo_ack_message":   PermissionMailboxAck,
	"karo_complete_task": PermissionTaskComplete,
	"karo_fail_task":     PermissionTaskFail,
	"karo_add_task":      PermissionTaskAdd,
	"karo_query_memory":  PermissionMemoryRead,
	"karo_store_memory":  PermissionMemoryWrite,
	"karo_report_status": PermissionStatusReport,
}
