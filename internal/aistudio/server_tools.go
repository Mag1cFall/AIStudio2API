package aistudio

import (
	"encoding/json"
	"fmt"
	"slices"
)

// serverToolTypes 是 Playground ToolCall 与 ToolResponse field 1 的工具类型编号，MCP 调用为 CUSTOM，MCP 结果不带类型
var serverToolTypes = map[string]int64{"": 0, "CUSTOM": 1, "GOOGLE_SEARCH_WEB": 2, "URL_CONTEXT": 3, "GOOGLE_MAPS": 8, "MEDIA_PROCESSING": 13}

// playgroundToolTypes 是 Playground 没有单独编号的工具类型在 Playground 中的表示：图片搜索步骤为网页搜索，File Search 不带类型
var playgroundToolTypes = map[string]string{"GOOGLE_SEARCH_IMAGE": "GOOGLE_SEARCH_WEB", "FILE_SEARCH": ""}

// serverSideInvocations 表示请求要开启 include_server_side_tool_invocations：客户端要求或内置工具与函数调用同时使用，图片模型的上游不支持该设置
func serverSideInvocations(tools Tools, imageRoute bool) bool {
	builtin := len(tools.Google) > 0 || tools.GoogleSearch != nil || len(tools.MCPServers) > 0 || len(tools.FileSearch) > 0
	return builtin && !imageRoute && tools.ToolConfig.Mode != "none" && (tools.ToolConfig.ServerSideInvocations || len(tools.Functions) > 0)
}

// playgroundServerSteps 表示 Playground 请求开启 include_server_side_tool_invocations：serverSideInvocations 成立或声明了 URL context，Playground 只在 URL context 工具步骤中给出各网址的读取结果
func playgroundServerSteps(tools Tools, imageRoute bool) bool {
	return serverSideInvocations(tools, imageRoute) || !imageRoute && tools.ToolConfig.Mode != "none" && slices.Contains(tools.Google, "url_context")
}

// urlContextGrounding 返回 URL context 工具结果中各网址的读取结果
func urlContextGrounding(step *ServerToolStep, path string, evidence json.RawMessage) (*GroundingMetadata, error) {
	var data struct {
		URLMetadata []struct {
			RetrievedURL string `json:"retrieved_url"`
			Status       string `json:"url_retrieval_status"`
		} `json:"url_metadata"`
	}
	if err := json.Unmarshal(step.Data, &data); err != nil {
		return nil, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "URL context 结果不是 url_metadata 对象", Raw: evidence}
	}
	grounding := &GroundingMetadata{}
	for _, item := range data.URLMetadata {
		grounding.URLContext = append(grounding.URLContext, URLMetadata{RetrievedURL: item.RetrievedURL, Status: item.Status})
	}
	return grounding, nil
}

// serverToolHidden 表示事件是客户端没有要求 includeServerSideToolInvocations 时的服务端工具步骤，视频理解处理步骤由上游按处理方式返回
func serverToolHidden(request GenerateRequest, event Event) bool {
	step := event.ToolCall
	if step == nil {
		step = event.ToolResponse
	}
	return step != nil && step.Type != "MEDIA_PROCESSING" && !request.Tools.ToolConfig.ServerSideInvocations
}

// serverToolReplayable 表示事件随重新生成在上游再次执行没有外部副作用：网页与图片搜索、网址上下文、地图与文件检索的调用和全部工具结果，MCP 调用与其他类型的调用除外
func serverToolReplayable(event Event) bool {
	return event.ToolCall == nil || slices.Contains([]string{"GOOGLE_SEARCH_WEB", "GOOGLE_SEARCH_IMAGE", "URL_CONTEXT", "GOOGLE_MAPS", "FILE_SEARCH"}, event.ToolCall.Type)
}

// encodeServerToolStep 编码 ToolCall 或 ToolResponse：工具类型、名称、Struct 与调用 ID
func encodeServerToolStep(step *ServerToolStep) ([]any, error) {
	stepType := step.Type
	if alias, ok := playgroundToolTypes[stepType]; ok {
		stepType = alias
	}
	code, ok := serverToolTypes[stepType]
	if !ok {
		return nil, fmt.Errorf("未知服务端工具类型 %q", step.Type)
	}
	wire := []any{nil, nil, nil, step.ID}
	if code != 0 {
		wire[0] = code
	}
	if step.Name != "" {
		wire[1] = step.Name
	}
	if len(step.Data) != 0 {
		value, err := encodeWireStructJSON(step.Data)
		if err != nil {
			return nil, fmt.Errorf("server tool %s data: %w", step.Type, err)
		}
		wire[2] = value
	}
	return wire, nil
}

// decodeServerToolStep 解码 ToolCall 或 ToolResponse
func decodeServerToolStep(raw json.RawMessage, path string, evidence json.RawMessage) (*ServerToolStep, error) {
	fields, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	var code int64
	if value := rawAt(fields, 0); !isJSONNull(value) {
		if code, err = rawInt64(value, path+"[0]", evidence); err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	step := &ServerToolStep{}
	known := false
	for name, value := range serverToolTypes {
		if value == code {
			step.Type, known = name, true
		}
	}
	if !known {
		return nil, &ProtocolEvidenceError{Method: "GenerateContent", Path: path + "[0]", Detail: fmt.Sprintf("未识别的服务端工具类型 %d", code), Raw: cloneRaw(evidence)}
	}
	if value := rawAt(fields, 1); !isJSONNull(value) {
		if step.Name, err = rawString(value, path+"[1]", evidence); err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	if value := rawAt(fields, 2); !isJSONNull(value) {
		if step.Data, err = decodeWireStruct(value, path+"[2]", evidence); err != nil {
			return nil, err
		}
	}
	if value := rawAt(fields, 3); !isJSONNull(value) {
		if step.ID, err = rawString(value, path+"[3]", evidence); err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	return step, nil
}
