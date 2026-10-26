package aistudio

import (
	"encoding/json"
	"fmt"
)

// encodeMediaProcessingStep encodes the observed MEDIA_PROCESSING tool message.
func encodeMediaProcessingStep(step *MediaProcessingStep) ([]any, error) {
	wire := []any{int64(13), nil, nil, step.ID}
	if step.Name != "" {
		wire[1] = step.Name
	}
	if len(step.Data) != 0 {
		value, err := encodeWireStructJSON(step.Data)
		if err != nil {
			return nil, fmt.Errorf("media processing payload: %w", err)
		}
		wire[2] = value
	}
	return wire, nil
}

// decodeMediaProcessingStep decodes the server-owned tool type observed as 13.
func decodeMediaProcessingStep(raw json.RawMessage, path string, evidence json.RawMessage) (*MediaProcessingStep, error) {
	fields, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	typeID, err := rawInt64(rawAt(fields, 0), path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	if typeID != 13 {
		return nil, nil
	}
	step := &MediaProcessingStep{}
	if value := rawAt(fields, 1); !isJSONNull(value) {
		step.Name, err = rawString(value, path+"[1]", evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	if value := rawAt(fields, 2); !isJSONNull(value) {
		step.Data, err = decodeWireStruct(value, path+"[2]", evidence)
		if err != nil {
			return nil, err
		}
	}
	if value := rawAt(fields, 3); !isJSONNull(value) {
		step.ID, err = rawString(value, path+"[3]", evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	return step, nil
}
