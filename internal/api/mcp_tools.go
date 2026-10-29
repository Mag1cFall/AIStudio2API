package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"

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

// listMCPTools 经出口代理以服务器的 URL 与头连接 MCP 服务器，读取 tools/list 返回的全部工具
func (s *server) listMCPTools(ctx context.Context, server aistudio.MCPServer) ([]*mcp.Tool, error) {
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
		return nil, fmt.Errorf("mcp server %q tools/list: %w", server.Name, err)
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
