export type ToolCall = Readonly<{ name: string; arguments: Readonly<Record<string, unknown>> }>;
export type AgentMessage = Readonly<{ role: "system" | "user"; content: string }> | Readonly<{ role: "assistant"; content: string; toolCalls?: readonly ToolCall[] }> | Readonly<{ role: "tool"; name: string; content: string }>;
export type ToolDefinition = Readonly<{ name: string; description: string; parameters: Readonly<Record<string, unknown>> }>;
export type ModelTurn = Readonly<{ content: string; toolCalls: readonly ToolCall[] }>;
