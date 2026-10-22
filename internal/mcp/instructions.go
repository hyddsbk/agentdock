package mcp

import "strings"

const (
	baseServerInstructions  = "优先调用 `agentdock_context` 获取可用于操作用户设备的核心能力、Skill、动态 MCP 和重要上下文。创建持久 Task 前先用 `task_manage admit` 冻结验收标准；Admission 通过后才创建并执行 Task。根据用户需求选择合适的能力检查、操作和验证设备状态。"
	nexusServerInstructions = "优先调用 `agentdock_context` 获取可用于操作用户设备的核心能力、Skill、动态 MCP、Workflow 模板、重要上下文和长期记忆索引。需要查找或读取长期记忆时使用 `recall_*`。创建持久 Task 前先用 `task_manage admit` 冻结标准；Admission=pass 后使用同一 admission_ref 调用 `workflow_template_manage match`，完成匹配后才 `task_manage create`。根据用户需求选择合适的能力检查、操作和验证设备状态。"
)

func serverInstructions(nexusEnabled bool, custom string) string {
	instructions := baseServerInstructions
	if nexusEnabled {
		instructions = nexusServerInstructions
	}
	custom = strings.TrimSpace(custom)
	if custom == "" {
		return instructions
	}
	return instructions + "\n\nAdditional operator instructions:\n" + custom
}
