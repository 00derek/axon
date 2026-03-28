// kernel/context.go
package kernel

// AgentContext holds the state visible to the agent during a turn:
// conversation messages and available tools.
type AgentContext struct {
	Messages []Message
	tools    []Tool
	disabled map[string]bool
}

// NewAgentContext creates a new AgentContext with the given tools.
func NewAgentContext(tools []Tool) *AgentContext {
	if tools == nil {
		tools = []Tool{}
	}
	return &AgentContext{
		tools:    tools,
		disabled: make(map[string]bool),
	}
}

// AddMessages appends messages to the conversation.
func (c *AgentContext) AddMessages(msgs ...Message) {
	c.Messages = append(c.Messages, msgs...)
}

// SystemPrompt returns the text of the first system message, or empty string.
func (c *AgentContext) SystemPrompt() string {
	for _, m := range c.Messages {
		if m.Role == RoleSystem {
			return m.TextContent()
		}
	}
	return ""
}

// SetSystemPrompt replaces the first system message or inserts one at the beginning.
func (c *AgentContext) SetSystemPrompt(prompt string) {
	for i, m := range c.Messages {
		if m.Role == RoleSystem {
			c.Messages[i] = SystemMsg(prompt)
			return
		}
	}
	// No existing system message — prepend
	c.Messages = append([]Message{SystemMsg(prompt)}, c.Messages...)
}

// LastUserMessage returns the last message with RoleUser, or nil.
func (c *AgentContext) LastUserMessage() *Message {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == RoleUser {
			return &c.Messages[i]
		}
	}
	return nil
}

// EnableTools sets only the named tools as active. All others are disabled.
func (c *AgentContext) EnableTools(names ...string) {
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	c.disabled = make(map[string]bool)
	for _, t := range c.tools {
		if !nameSet[t.Name()] {
			c.disabled[t.Name()] = true
		}
	}
}

// DisableTools marks the named tools as inactive.
func (c *AgentContext) DisableTools(names ...string) {
	for _, n := range names {
		c.disabled[n] = true
	}
}

// AddTools registers new tools (active by default).
func (c *AgentContext) AddTools(tools ...Tool) {
	c.tools = append(c.tools, tools...)
}

// ActiveTools returns only the currently enabled tools.
func (c *AgentContext) ActiveTools() []Tool {
	var active []Tool
	for _, t := range c.tools {
		if !c.disabled[t.Name()] {
			active = append(active, t)
		}
	}
	return active
}

// ReplaceTools replaces the entire tool set and resets the disabled set.
func (c *AgentContext) ReplaceTools(tools []Tool) {
	c.tools = tools
	c.disabled = make(map[string]bool)
}

// AllTools returns all registered tools regardless of enabled state.
func (c *AgentContext) AllTools() []Tool {
	return c.tools
}

// GetTool returns a tool by name from all registered tools.
func (c *AgentContext) GetTool(name string) (Tool, bool) {
	for _, t := range c.tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}
