package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// mcpHeaderTransport 为发往 MCP 服务器主机的请求加上请求给出的头
type mcpHeaderTransport struct {
	base    http.RoundTripper
	host    string
	headers map[string]string
}

func (t mcpHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != t.host {
		return t.base.RoundTrip(request)
	}
	request = request.Clone(request.Context())
	for name, value := range t.headers {
		request.Header.Set(name, value)
	}
	return t.base.RoundTrip(request)
}

// resolveMCPTools 按 tools/list 把带只读或排除要求的 MCP 服务器换成允许的工具名，没有允许工具的服务器不发送
func (s *server) resolveMCPTools(ctx context.Context, request aistudio.GenerateRequest) (aistudio.GenerateRequest, error) {
	if !slices.ContainsFunc(request.Tools.MCPServers, func(server aistudio.MCPServer) bool {
		return server.ReadOnly || len(server.DisabledTools) > 0
	}) {
		return request, nil
	}
	servers := make([]aistudio.MCPServer, 0, len(request.Tools.MCPServers))
	for _, server := range request.Tools.MCPServers {
		if !server.ReadOnly && len(server.DisabledTools) == 0 {
			servers = append(servers, server)
			continue
		}
		listed, err := s.listMCPTools(ctx, server)
		if err != nil {
			return request, err
		}
		var allowed []string
		for _, tool := range listed {
			readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
			if server.ReadOnly && !readOnly || slices.Contains(server.DisabledTools, tool.Name) ||
				server.AllowedTools != nil && !slices.Contains(server.AllowedTools, tool.Name) {
				continue
			}
			allowed = append(allowed, tool.Name)
		}
		if len(allowed) == 0 {
			continue
		}
		server.AllowedTools, server.ReadOnly, server.DisabledTools = allowed, false, nil
		servers = append(servers, server)
	}
	request.Tools.MCPServers = servers
	request.Tools.ToolConfig.ServerSideInvocations = len(servers) > 0
	return request, nil
}

// connectMCP 经出口代理以服务器的 URL 与头连接 MCP 服务器，连接错误以随后调用的方法名说明
func (s *server) connectMCP(ctx context.Context, server aistudio.MCPServer, method string) (*mcp.ClientSession, error) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	if s.config.EgressProxy != nil {
		if proxy := s.config.EgressProxy(); proxy != "" {
			proxyURL, err := url.Parse(proxy)
			if err != nil {
				return nil, fmt.Errorf("mcp server %q egress proxy: %w", server.Name, err)
			}
			base.Proxy = http.ProxyURL(proxyURL)
		}
	}
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q url: %w", server.Name, err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "aistudio2api"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: mcpHeaderTransport{base: base, host: endpoint.Host, headers: server.Headers}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q %s: %w", server.Name, method, err)
	}
	return session, nil
}

// listMCPTools 读取 MCP 服务器 tools/list 返回的全部工具
func (s *server) listMCPTools(ctx context.Context, server aistudio.MCPServer) ([]*mcp.Tool, error) {
	session, err := s.connectMCP(ctx, server, "tools/list")
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var tools []*mcp.Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("mcp server %q tools/list: %w", server.Name, err)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// callMCPTool 以 JSON 参数调用 MCP 服务器上的工具，返回文本内容拼接的输出与工具是否报告错误
func (s *server) callMCPTool(ctx context.Context, server aistudio.MCPServer, name string, arguments string) (string, bool, error) {
	var values map[string]any
	if err := json.Unmarshal([]byte(arguments), &values); err != nil {
		return "", false, fmt.Errorf("mcp tool %q arguments: %w", name, err)
	}
	session, err := s.connectMCP(ctx, server, "tools/call "+name)
	if err != nil {
		return "", false, err
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: values})
	if err != nil {
		return "", false, fmt.Errorf("mcp server %q tools/call %s: %w", server.Name, name, err)
	}
	var output strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			output.WriteString(text.Text)
		}
	}
	if output.Len() == 0 && result.StructuredContent != nil {
		encoded, _ := json.Marshal(result.StructuredContent)
		output.Write(encoded)
	}
	return output.String(), result.IsError, nil
}

// mcpApprovalFilter 是 require_approval 对象中 always 或 never 的工具过滤，给出的条件同时满足时匹配
type mcpApprovalFilter struct {
	ToolNames []string `json:"tool_names"`
	ReadOnly  *bool    `json:"read_only"`
}

// matches 返回过滤给出条件且工具满足全部条件
func (filter *mcpApprovalFilter) matches(tool *mcp.Tool) bool {
	if filter == nil || len(filter.ToolNames) == 0 && filter.ReadOnly == nil {
		return false
	}
	readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
	return (len(filter.ToolNames) == 0 || slices.Contains(filter.ToolNames, tool.Name)) && (filter.ReadOnly == nil || *filter.ReadOnly == readOnly)
}

// mcpApproval 是 mcp 工具的 require_approval：省略与 always 时全部工具需要批准，never 时全部不需要，对象按 always 与 never 过滤，两者都不匹配的工具需要批准
type mcpApproval struct {
	never  bool
	always *mcpApprovalFilter
	skip   *mcpApprovalFilter
}

// parseMCPApproval 解析 require_approval 的字符串或过滤对象
func parseMCPApproval(raw json.RawMessage) (mcpApproval, error) {
	if !rawJSONConfigured(raw) {
		return mcpApproval{}, nil
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		switch value {
		case "always":
			return mcpApproval{}, nil
		case "never":
			return mcpApproval{never: true}, nil
		}
		return mcpApproval{}, fmt.Errorf("mcp require_approval must be always, never or a filter object")
	}
	var filter struct {
		Always *mcpApprovalFilter `json:"always"`
		Never  *mcpApprovalFilter `json:"never"`
	}
	if err := json.Unmarshal(raw, &filter); err != nil {
		return mcpApproval{}, fmt.Errorf("mcp require_approval: %w", err)
	}
	return mcpApproval{always: filter.Always, skip: filter.Never}, nil
}

// required 返回调用该工具前是否需要批准
func (approval mcpApproval) required(tool *mcp.Tool) bool {
	return !approval.never && (approval.always.matches(tool) || !approval.skip.matches(tool))
}

// mcpApprovalFunction 是需要批准的 MCP 工具声明给模型的函数名
func mcpApprovalFunction(label string, tool string) string {
	return "mcp__" + label + "__" + tool
}

// mcpApprovalTools 把需要批准的 MCP 工具声明为函数，由服务在批准后调用；同一服务器上不需要批准的工具仍交给上游直接调用；tool_choice 为 none 或指定函数时不展开，限定工具时只展开限定的服务器与工具
func (s *server) mcpApprovalTools(ctx context.Context, tools []responsesTool, choice json.RawMessage, request aistudio.GenerateRequest) (aistudio.GenerateRequest, error) {
	config, allowed, err := openAIToolChoice(choice)
	if err != nil || config.Mode == "none" || allowed == nil && len(config.AllowedFunctionNames) > 0 {
		return request, err
	}
	restricted := allowedMCPServers(allowed)
	for _, tool := range tools {
		if tool.Type != "mcp" {
			continue
		}
		approval, err := parseMCPApproval(tool.RequireApproval)
		if err != nil || approval.never {
			continue
		}
		server, err := responsesMCPServer(tool)
		if err != nil {
			return request, err
		}
		if allowed != nil {
			names, ok := restricted[server.Name]
			if !ok {
				continue
			}
			if names != nil {
				server.AllowedTools = names
			}
		}
		listed, err := s.listMCPTools(ctx, server)
		if err != nil {
			return request, err
		}
		var direct []string
		for _, item := range listed {
			readOnly := item.Annotations != nil && item.Annotations.ReadOnlyHint
			if slices.Contains(server.DisabledTools, item.Name) || server.AllowedTools != nil && !slices.Contains(server.AllowedTools, item.Name) || server.ReadOnly && !readOnly {
				continue
			}
			if !approval.required(item) {
				direct = append(direct, item.Name)
				continue
			}
			name := mcpApprovalFunction(server.Name, item.Name)
			parameters, _ := json.Marshal(item.InputSchema)
			request.Tools.Functions = append(request.Tools.Functions, aistudio.FunctionDeclaration{Name: name, Description: item.Description, Parameters: parameters})
			if len(request.Tools.ToolConfig.AllowedFunctionNames) > 0 {
				request.Tools.ToolConfig.AllowedFunctionNames = append(request.Tools.ToolConfig.AllowedFunctionNames, name)
			}
		}
		if len(direct) > 0 {
			server.AllowedTools, server.ReadOnly, server.DisabledTools = direct, false, nil
			request.Tools.MCPServers = append(request.Tools.MCPServers, server)
			request.Tools.ToolConfig.ServerSideInvocations = true
		}
	}
	return request, nil
}

// mcpApprovalItem 是输入与已保存输出中与 MCP 批准有关的项
type mcpApprovalItem struct {
	Type              string          `json:"type"`
	ID                string          `json:"id"`
	ServerLabel       string          `json:"server_label"`
	Name              string          `json:"name"`
	Arguments         string          `json:"arguments"`
	ApprovalRequestID string          `json:"approval_request_id"`
	Approve           bool            `json:"approve"`
	Reason            string          `json:"reason"`
	Output            *string         `json:"output"`
	Error             json.RawMessage `json:"error"`
}

// mcpApprovalCallID 是批准请求对应的函数调用 ID，批准请求 ID 为 mcpr_ 加调用 ID
func mcpApprovalCallID(id string) string {
	return strings.TrimPrefix(id, "mcpr_")
}

// mcpApprovalTarget 返回需要批准的 MCP 工具函数名对应的服务器标签与工具名
func mcpApprovalTarget(tools []responsesTool, name string) (string, string, bool) {
	for _, tool := range tools {
		if tool.Type != "mcp" {
			continue
		}
		if rest, ok := strings.CutPrefix(name, mcpApprovalFunction(tool.ServerLabel, "")); ok {
			return tool.ServerLabel, rest, true
		}
	}
	return "", "", false
}

// applyMCPApprovals 把输入中的批准请求改写为函数调用、批准响应改写为函数结果：execute 为真时批准的调用由服务执行，已有 mcp_call 结果的不再执行；返回改写后的输入与排在输出首位的 mcp_call 项
func (s *server) applyMCPApprovals(ctx context.Context, previousID string, tools []responsesTool, input json.RawMessage, execute bool) (json.RawMessage, []map[string]any, error) {
	var raws []json.RawMessage
	if json.Unmarshal(input, &raws) != nil {
		return input, nil, nil
	}
	items := make([]mcpApprovalItem, len(raws))
	for index, raw := range raws {
		_ = json.Unmarshal(raw, &items[index])
	}
	if !slices.ContainsFunc(items, func(item mcpApprovalItem) bool { return item.Type == "mcp_approval_response" }) {
		return input, nil, nil
	}
	requests := map[string]mcpApprovalItem{}
	executed := map[string]mcpApprovalItem{}
	if previousID != "" {
		raw, _, _ := s.responseStates.Response(previousID)
		var previous struct {
			Output []mcpApprovalItem `json:"output"`
		}
		_ = json.Unmarshal(raw, &previous)
		for _, item := range previous.Output {
			if item.Type == "mcp_approval_request" {
				requests[item.ID] = item
			}
		}
	}
	for _, item := range items {
		switch {
		case item.Type == "mcp_approval_request":
			requests[item.ID] = item
		case item.Type == "mcp_call" && item.ApprovalRequestID != "":
			executed[item.ApprovalRequestID] = item
		}
	}
	rewritten := make([]any, 0, len(raws))
	var calls []map[string]any
	for index, item := range items {
		switch {
		case item.Type == "mcp_approval_request":
			rewritten = append(rewritten, map[string]any{
				"type": "function_call", "call_id": mcpApprovalCallID(item.ID), "name": mcpApprovalFunction(item.ServerLabel, item.Name), "arguments": item.Arguments,
			})
		case item.Type == "mcp_call" && item.ApprovalRequestID != "":
		case item.Type == "mcp_approval_response":
			approval, ok := requests[item.ApprovalRequestID]
			if !ok {
				return nil, nil, fmt.Errorf("mcp_approval_response: approval request %q was not found", item.ApprovalRequestID)
			}
			output := map[string]any{"error": strings.TrimSpace("The user denied this MCP tool call. " + item.Reason)}
			if done, ok := executed[item.ApprovalRequestID]; ok {
				output = map[string]any{"output": done.Output, "error": rawJSONValue(done.Error, nil)}
			} else if item.Approve && !execute {
				output = map[string]any{"output": nil, "error": nil}
			} else if item.Approve {
				call, err := s.approvedMCPCall(ctx, tools, approval)
				if err != nil {
					return nil, nil, err
				}
				calls = append(calls, call)
				output = map[string]any{"output": call["output"], "error": call["error"]}
			}
			encoded, _ := json.Marshal(output)
			rewritten = append(rewritten, map[string]any{"type": "function_call_output", "call_id": mcpApprovalCallID(item.ApprovalRequestID), "output": string(encoded)})
		default:
			rewritten = append(rewritten, raws[index])
		}
	}
	encoded, err := json.Marshal(rewritten)
	return encoded, calls, err
}

// approvedMCPCall 在请求声明的同名 MCP 服务器上执行批准的调用，返回 mcp_call 输出项；连接与协议错误记为 mcp_protocol_error，工具报告的错误记为 mcp_tool_execution_error
func (s *server) approvedMCPCall(ctx context.Context, tools []responsesTool, approval mcpApprovalItem) (map[string]any, error) {
	index := slices.IndexFunc(tools, func(tool responsesTool) bool { return tool.Type == "mcp" && tool.ServerLabel == approval.ServerLabel })
	if index < 0 {
		return nil, fmt.Errorf("mcp_approval_response: mcp server %q is not in tools", approval.ServerLabel)
	}
	server, err := responsesMCPServer(tools[index])
	if err != nil {
		return nil, err
	}
	item := map[string]any{
		"id": "mcp_" + mcpApprovalCallID(approval.ID), "type": "mcp_call", "status": "completed", "approval_request_id": approval.ID,
		"server_label": approval.ServerLabel, "name": approval.Name, "arguments": approval.Arguments, "output": nil, "error": nil,
	}
	output, failed, err := s.callMCPTool(ctx, server, approval.Name, approval.Arguments)
	switch {
	case err != nil:
		item["status"], item["error"] = "failed", map[string]any{"type": "mcp_protocol_error", "code": -32603, "message": err.Error()}
	case failed:
		item["status"], item["error"] = "failed", map[string]any{"type": "mcp_tool_execution_error", "content": []any{map[string]any{"type": "text", "text": output}}}
	default:
		item["output"] = output
	}
	return item, nil
}
