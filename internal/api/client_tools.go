package api

import (
	"encoding/json"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// customToolParameters 是 custom 工具作为函数声明时唯一的字符串参数
var customToolParameters = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string","description":"The raw input passed to the tool"}},"required":["input"]}`)

// clientToolDeclarations 是 Responses 客户端执行工具对应的固定函数声明
var clientToolDeclarations = map[string]aistudio.FunctionDeclaration{
	"local_shell": {
		Name:        "local_shell",
		Description: "Runs a command on the user's machine and returns its output.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"},"description":"The program and its arguments"},"working_directory":{"type":"string","description":"The working directory to run the command in"},"timeout_ms":{"type":"integer","description":"The timeout for the command in milliseconds"}},"required":["command"]}`),
	},
	"shell": {
		Name:        "shell",
		Description: "Runs shell commands in the user's environment and returns their output.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"commands":{"type":"array","items":{"type":"string"},"description":"Shell commands to run in order"},"timeout_ms":{"type":"integer","description":"The timeout for the commands in milliseconds"},"max_output_length":{"type":"integer","description":"The maximum number of output characters to return"}},"required":["commands"]}`),
	},
	"apply_patch": {
		Name:        "apply_patch",
		Description: "Creates, updates or deletes a file. diff uses the V4A format: for create_file every line of the new file starts with +; for update_file each hunk starts with @@ and every line starts with a space for context, - for a removed line or + for an added line.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"type":{"type":"string","enum":["create_file","update_file","delete_file"],"description":"The file operation"},"path":{"type":"string","description":"The file path"},"diff":{"type":"string","description":"The V4A diff for create_file and update_file"}},"required":["type","path"]}`),
	},
}

// customToolDeclaration 将 custom 工具声明为只接收字符串 input 的函数，语法格式写入描述
func customToolDeclaration(name string, description string, format json.RawMessage) aistudio.FunctionDeclaration {
	var spec struct {
		Type       string `json:"type"`
		Syntax     string `json:"syntax"`
		Definition string `json:"definition"`
		Grammar    *struct {
			Syntax     string `json:"syntax"`
			Definition string `json:"definition"`
		} `json:"grammar"`
	}
	_ = json.Unmarshal(format, &spec)
	if spec.Grammar != nil {
		spec.Syntax, spec.Definition = spec.Grammar.Syntax, spec.Grammar.Definition
	}
	if spec.Type == "grammar" && spec.Definition != "" {
		if description != "" {
			description += "\n\n"
		}
		description += "The input must match this " + spec.Syntax + " grammar:\n" + spec.Definition
	}
	return aistudio.FunctionDeclaration{Name: name, Description: description, Parameters: customToolParameters}
}

// customToolArguments 把 custom 工具的原始输入包装为函数参数
func customToolArguments(input string) json.RawMessage {
	encoded, _ := json.Marshal(map[string]string{"input": input})
	return encoded
}

// customToolInput 取出函数参数中的 input 字符串，缺失时返回参数原文
func customToolInput(arguments json.RawMessage) string {
	var value struct {
		Input *string `json:"input"`
	}
	if json.Unmarshal(arguments, &value) == nil && value.Input != nil {
		return *value.Input
	}
	return string(arguments)
}

// functionCallArguments 返回历史调用参数，非 JSON object 的原文包装为 _raw 字段
func functionCallArguments(arguments string) json.RawMessage {
	if arguments == "" {
		return json.RawMessage(`{}`)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &object) == nil && object != nil {
		return json.RawMessage(arguments)
	}
	encoded, _ := json.Marshal(map[string]string{"_raw": arguments})
	return encoded
}

// responsesToolKind 返回函数名对应的 Responses 工具类型，普通函数返回 function
func responsesToolKind(tools []responsesTool, name string) string {
	for _, tool := range tools {
		switch tool.Type {
		case "custom":
			if tool.Name == name {
				return "custom"
			}
		case "local_shell", "shell", "apply_patch":
			if tool.Type == name {
				return tool.Type
			}
		case "namespace":
			for _, inner := range tool.Tools {
				if inner.Type == "custom" && tool.Name+"."+inner.Name == name {
					return "custom"
				}
			}
		}
	}
	return "function"
}

// localShellAction 把 local_shell 函数参数补成 exec 动作
func localShellAction(arguments json.RawMessage) map[string]any {
	action := map[string]any{}
	_ = json.Unmarshal(arguments, &action)
	action["type"] = "exec"
	if action["env"] == nil {
		action["env"] = map[string]string{}
	}
	return action
}

// shellAction 把 shell 函数参数补成带空上限字段的动作
func shellAction(arguments json.RawMessage) map[string]any {
	action := map[string]any{"timeout_ms": nil, "max_output_length": nil}
	_ = json.Unmarshal(arguments, &action)
	return action
}

// localShellArguments 去掉 exec 动作的类型字段作为函数参数
func localShellArguments(action json.RawMessage) json.RawMessage {
	values := map[string]json.RawMessage{}
	_ = json.Unmarshal(action, &values)
	delete(values, "type")
	encoded, _ := json.Marshal(values)
	return encoded
}

// rawObjectArguments 返回 JSON object 形式的动作或操作，缺失时为空对象
func rawObjectArguments(raw json.RawMessage) json.RawMessage {
	if !rawJSONConfigured(raw) {
		return json.RawMessage(`{}`)
	}
	return raw
}

// chatCustomTool 返回函数名是否属于 Chat custom 工具
func chatCustomTool(tools []openAITool, name string) bool {
	for _, tool := range tools {
		if tool.Type == "custom" && tool.Custom.Name == name {
			return true
		}
	}
	return false
}
