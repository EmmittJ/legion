package acp

// Helper functions for constructing discriminated unions.
//
// These functions make it easier to create the various union types
// without needing to understand the internal variant structure.

// --- SessionUpdate constructors ---

// NewSessionUpdateAgentMessageChunk creates a SessionUpdate with an agent message chunk.
func NewSessionUpdateAgentMessageChunk(content ContentBlock) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateAgentMessageChunk{
		ContentChunk: ContentChunk{
			Content: content,
		},
		SessionUpdate: "agent_message_chunk",
	}}
}

// NewSessionUpdateUserMessageChunk creates a SessionUpdate with a user message chunk.
func NewSessionUpdateUserMessageChunk(content ContentBlock) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateUserMessageChunk{
		ContentChunk: ContentChunk{
			Content: content,
		},
		SessionUpdate: "user_message_chunk",
	}}
}

// NewSessionUpdateAgentThoughtChunk creates a SessionUpdate with an agent thought chunk.
func NewSessionUpdateAgentThoughtChunk(content ContentBlock) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateAgentThoughtChunk{
		ContentChunk: ContentChunk{
			Content: content,
		},
		SessionUpdate: "agent_thought_chunk",
	}}
}

// NewSessionUpdateToolCall creates a SessionUpdate with a tool call.
func NewSessionUpdateToolCall(toolCall ToolCall) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateToolCall{
		ToolCall:      toolCall,
		SessionUpdate: "tool_call",
	}}
}

// NewSessionUpdateToolCallUpdate creates a SessionUpdate with a tool call update.
func NewSessionUpdateToolCallUpdate(update ToolCallUpdate) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateToolCallUpdate{
		ToolCallUpdate: update,
		SessionUpdate:  "tool_call_update",
	}}
}

// NewSessionUpdatePlan creates a SessionUpdate with a plan.
func NewSessionUpdatePlan(entries []PlanEntry) SessionUpdate {
	return SessionUpdate{variant: SessionUpdatePlan{
		Plan: Plan{
			Entries: entries,
		},
		SessionUpdate: "plan",
	}}
}

// NewSessionUpdateAvailableCommandsUpdate creates a SessionUpdate with available commands.
func NewSessionUpdateAvailableCommandsUpdate(commands []AvailableCommand) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateAvailableCommandsUpdate{
		AvailableCommandsUpdate: AvailableCommandsUpdate{
			AvailableCommands: commands,
		},
		SessionUpdate: "available_commands_update",
	}}
}

// NewSessionUpdateCurrentModeUpdate creates a SessionUpdate with a mode change.
func NewSessionUpdateCurrentModeUpdate(modeId SessionModeID) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateCurrentModeUpdate{
		CurrentModeUpdate: CurrentModeUpdate{
			CurrentModeID: modeId,
		},
		SessionUpdate: "current_mode_update",
	}}
}

// NewSessionUpdateConfigOptionUpdate creates a SessionUpdate with config option changes.
func NewSessionUpdateConfigOptionUpdate(options []SessionConfigOption) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateConfigOptionUpdate{
		ConfigOptionUpdate: ConfigOptionUpdate{
			ConfigOptions: options,
		},
		SessionUpdate: "config_option_update",
	}}
}

// NewSessionUpdateSessionInfoUpdate creates a SessionUpdate with session info changes.
func NewSessionUpdateSessionInfoUpdate(title string, updatedAt string) SessionUpdate {
	return SessionUpdate{variant: SessionUpdateSessionInfoUpdate{
		SessionInfoUpdate: SessionInfoUpdate{
			Title:     title,
			UpdatedAt: updatedAt,
		},
		SessionUpdate: "session_info_update",
	}}
}

// --- ContentBlock constructors ---

// NewContentBlockText creates a text content block.
func NewContentBlockText(text string) ContentBlock {
	return ContentBlock{variant: ContentBlockText{
		TextContent: TextContent{
			Text: text,
		},
		Type: "text",
	}}
}

// NewContentBlockImage creates an image content block.
func NewContentBlockImage(data, mimeType string, uri string) ContentBlock {
	return ContentBlock{variant: ContentBlockImage{
		ImageContent: ImageContent{
			Data:     data,
			MimeType: mimeType,
			URI:      uri,
		},
		Type: "image",
	}}
}

// NewContentBlockAudio creates an audio content block.
func NewContentBlockAudio(data, mimeType string) ContentBlock {
	return ContentBlock{variant: ContentBlockAudio{
		AudioContent: AudioContent{
			Data:     data,
			MimeType: mimeType,
		},
		Type: "audio",
	}}
}

// NewContentBlockResourceLink creates a resource link content block.
func NewContentBlockResourceLink(name, uri string, title, description, mimeType string, size *int64) ContentBlock {
	return ContentBlock{variant: ContentBlockResourceLink{
		ResourceLink: ResourceLink{
			Name:        name,
			URI:         uri,
			Title:       title,
			Description: description,
			Size:        size,
			MimeType:    mimeType,
		},
		Type: "resource_link",
	}}
}

// NewContentBlockResource creates an embedded resource content block.
func NewContentBlockResource(resource EmbeddedResource) ContentBlock {
	return ContentBlock{variant: ContentBlockResource{
		EmbeddedResource: resource,
		Type:             "resource",
	}}
}

// --- RequestPermissionOutcome constructors ---

// NewRequestPermissionOutcomeSelected creates a selected permission outcome.
func NewRequestPermissionOutcomeSelected(optionId PermissionOptionID) RequestPermissionOutcome {
	return RequestPermissionOutcome{variant: RequestPermissionOutcomeSelected{
		SelectedPermissionOutcome: SelectedPermissionOutcome{
			OptionID: optionId,
		},
		Outcome: "selected",
	}}
}

// NewRequestPermissionOutcomeCancelled creates a cancelled permission outcome.
func NewRequestPermissionOutcomeCancelled() RequestPermissionOutcome {
	return RequestPermissionOutcome{variant: RequestPermissionOutcomeCancelled{
		Outcome: "cancelled",
	}}
}

// --- ToolCallContent constructors ---

// NewToolCallContentContent creates tool call content with a content block.
func NewToolCallContentContent(contentBlock ContentBlock) ToolCallContent {
	return ToolCallContent{variant: ToolCallContentContent{
		Content: Content{
			Content: contentBlock,
		},
		Type: "content",
	}}
}

// NewToolCallContentDiff creates tool call content with a diff.
func NewToolCallContentDiff(path, newText, oldText string) ToolCallContent {
	return ToolCallContent{variant: ToolCallContentDiff{
		Diff: Diff{
			Path:    path,
			NewText: newText,
			OldText: oldText,
		},
		Type: "diff",
	}}
}

// NewToolCallContentTerminal creates tool call content with a terminal reference.
func NewToolCallContentTerminal(terminalId string) ToolCallContent {
	return ToolCallContent{variant: ToolCallContentTerminal{
		Terminal: Terminal{
			TerminalID: terminalId,
		},
		Type: "terminal",
	}}
}

// --- MCPServer constructors ---

// NewMCPServerStdio creates an MCP server with stdio transport.
func NewMCPServerStdio(name, command string, args []string, env []EnvVariable) MCPServer {
	return MCPServer{variant: MCPServerStdio{
		Name:    name,
		Command: command,
		Args:    args,
		Env:     env,
	}}
}

// NewMCPServerHTTP creates an MCP server with HTTP transport.
func NewMCPServerHTTP(name, url string, headers []HTTPHeader) MCPServer {
	return MCPServer{variant: MCPServerHTTP{
		Name:    name,
		URL:     url,
		Headers: headers,
	}}
}

// NewMCPServerSSE creates an MCP server with SSE transport.
func NewMCPServerSSE(name, url string, headers []HTTPHeader) MCPServer {
	return MCPServer{variant: MCPServerSSE{
		Name:    name,
		URL:     url,
		Headers: headers,
	}}
}

