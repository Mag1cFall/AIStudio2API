package aistudio

import "fmt"

func validateRequestedTools(tools Tools, model Model) error {
	if tools.ToolConfig.Mode == "none" {
		return nil
	}
	if len(tools.Functions) > 0 && !model.Capabilities["function_declarations"] {
		return fmt.Errorf("model %q does not support function declarations", model.ID)
	}
	if tools.GoogleSearch != nil {
		if (tools.GoogleSearch.WebSearch || !tools.GoogleSearch.ImageSearch) && !model.Capabilities["google_search"] {
			return fmt.Errorf("model %q does not support google_search", model.ID)
		}
		if tools.GoogleSearch.ImageSearch && !model.Capabilities["image_search"] {
			return fmt.Errorf("model %q does not support image_search", model.ID)
		}
	}
	hasMaps := false
	hasCode := false
	hasURLContext := false
	for _, tool := range tools.Google {
		capability := ""
		switch tool {
		case "code_execution", "google_search", "image_search", "google_maps":
			capability = tool
		case "url_context":
			capability = "browse"
		default:
			return fmt.Errorf("unknown Google tool %q", tool)
		}
		if !model.Capabilities[capability] {
			return fmt.Errorf("model %q does not support %s", model.ID, tool)
		}
		hasMaps = hasMaps || tool == "google_maps"
		hasCode = hasCode || tool == "code_execution"
		hasURLContext = hasURLContext || tool == "url_context"
	}
	if hasMaps && (hasCode || hasURLContext) {
		return fmt.Errorf("google_maps cannot be used together with code_execution or url_context")
	}
	return nil
}
