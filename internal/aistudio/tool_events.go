package aistudio

import (
	"encoding/json"
	"fmt"
)

func decodeExecutableCode(raw json.RawMessage, path string, evidence json.RawMessage) (ExecutableCode, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return ExecutableCode{}, withMethod(err, "GenerateContent")
	}
	if len(values) < 2 {
		return ExecutableCode{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "executable code 字段不足", Raw: raw}
	}
	languageCode, err := rawInt64(values[0], path+"[0]", evidence)
	if err != nil {
		return ExecutableCode{}, withMethod(err, "GenerateContent")
	}
	language, ok := map[int64]string{0: "LANGUAGE_UNSPECIFIED", 1: "PYTHON"}[languageCode]
	if !ok {
		return ExecutableCode{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path + "[0]", Detail: fmt.Sprintf("未识别的 executable code language %d", languageCode), Raw: raw}
	}
	code, err := rawString(values[1], path+"[1]", evidence)
	if err != nil {
		return ExecutableCode{}, withMethod(err, "GenerateContent")
	}
	return ExecutableCode{Language: language, Code: code}, nil
}

func decodeCodeExecutionResult(raw json.RawMessage, path string, evidence json.RawMessage) (CodeExecutionResult, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return CodeExecutionResult{}, withMethod(err, "GenerateContent")
	}
	if len(values) == 0 {
		return CodeExecutionResult{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "code execution result 字段不足", Raw: raw}
	}
	outcomeCode, err := rawInt64(values[0], path+"[0]", evidence)
	if err != nil {
		return CodeExecutionResult{}, withMethod(err, "GenerateContent")
	}
	outcome, ok := map[int64]string{
		0: "OUTCOME_UNSPECIFIED",
		1: "OUTCOME_OK",
		2: "OUTCOME_FAILED",
		3: "OUTCOME_DEADLINE_EXCEEDED",
	}[outcomeCode]
	if !ok {
		return CodeExecutionResult{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path + "[0]", Detail: fmt.Sprintf("未识别的 code execution outcome %d", outcomeCode), Raw: raw}
	}
	output := ""
	if outputRaw := rawAt(values, 1); !isJSONNull(outputRaw) {
		output, err = rawString(outputRaw, path+"[1]", evidence)
		if err != nil {
			return CodeExecutionResult{}, withMethod(err, "GenerateContent")
		}
	}
	return CodeExecutionResult{Outcome: outcome, Output: output}, nil
}

func decodeGroundingMetadata(raw json.RawMessage, path string, evidence json.RawMessage) (GroundingMetadata, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return GroundingMetadata{}, withMethod(err, "GenerateContent")
	}
	metadata := GroundingMetadata{}
	if entryRaw := rawAt(values, 0); !isJSONNull(entryRaw) {
		entry, err := decodeSearchEntryPoint(entryRaw, path+"[0]", evidence)
		if err != nil {
			return GroundingMetadata{}, err
		}
		metadata.SearchEntryPoint = &entry
	}
	if chunksRaw := rawAt(values, 1); !isJSONNull(chunksRaw) {
		metadata.Chunks, err = decodeGroundingChunks(chunksRaw, path+"[1]", evidence)
		if err != nil {
			return GroundingMetadata{}, err
		}
	}
	if supportsRaw := rawAt(values, 2); !isJSONNull(supportsRaw) {
		metadata.Supports, err = decodeGroundingSupports(supportsRaw, path+"[2]", evidence)
		if err != nil {
			return GroundingMetadata{}, err
		}
	}
	if retrievalRaw := rawAt(values, 3); !isJSONNull(retrievalRaw) {
		retrieval, err := rawArray(retrievalRaw, path+"[3]", evidence)
		if err != nil {
			return GroundingMetadata{}, withMethod(err, "GenerateContent")
		}
		if scoreRaw := rawAt(retrieval, 1); !isJSONNull(scoreRaw) {
			score, err := rawFloat64(scoreRaw, path+"[3][1]", evidence)
			if err != nil {
				return GroundingMetadata{}, withMethod(err, "GenerateContent")
			}
			metadata.DynamicRetrievalScore = &score
		}
	}
	if queriesRaw := rawAt(values, 4); !isJSONNull(queriesRaw) {
		queries, err := rawArray(queriesRaw, path+"[4]", evidence)
		if err != nil {
			return GroundingMetadata{}, withMethod(err, "GenerateContent")
		}
		metadata.WebSearchQueries = make([]string, 0, len(queries))
		for index, queryRaw := range queries {
			query, err := rawString(queryRaw, fmt.Sprintf("%s[4][%d]", path, index), evidence)
			if err != nil {
				return GroundingMetadata{}, withMethod(err, "GenerateContent")
			}
			metadata.WebSearchQueries = append(metadata.WebSearchQueries, query)
		}
	}
	if tokenRaw := rawAt(values, 6); !isJSONNull(tokenRaw) {
		metadata.MapsWidgetContextToken, err = rawString(tokenRaw, path+"[6]", evidence)
		if err != nil {
			return GroundingMetadata{}, withMethod(err, "GenerateContent")
		}
	}
	return metadata, nil
}

func decodeSearchEntryPoint(raw json.RawMessage, path string, evidence json.RawMessage) (SearchEntryPoint, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return SearchEntryPoint{}, withMethod(err, "GenerateContent")
	}
	entry := SearchEntryPoint{}
	if renderedRaw := rawAt(values, 0); !isJSONNull(renderedRaw) {
		entry.RenderedContent, err = rawString(renderedRaw, path+"[0]", evidence)
		if err != nil {
			return SearchEntryPoint{}, withMethod(err, "GenerateContent")
		}
	}
	if blobRaw := rawAt(values, 1); !isJSONNull(blobRaw) {
		entry.SDKBlob, err = rawString(blobRaw, path+"[1]", evidence)
		if err != nil {
			return SearchEntryPoint{}, withMethod(err, "GenerateContent")
		}
	}
	return entry, nil
}

func decodeGroundingChunks(raw json.RawMessage, path string, evidence json.RawMessage) ([]GroundingChunk, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	chunks := make([]GroundingChunk, 0, len(values))
	for index, rawChunk := range values {
		chunkPath := fmt.Sprintf("%s[%d]", path, index)
		chunk, err := decodeGroundingChunk(rawChunk, chunkPath, evidence)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func decodeGroundingChunk(raw json.RawMessage, path string, evidence json.RawMessage) (GroundingChunk, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return GroundingChunk{}, withMethod(err, "GenerateContent")
	}
	variant := -1
	for index := 0; index < 3; index++ {
		if !isJSONNull(rawAt(values, index)) {
			if variant >= 0 {
				return GroundingChunk{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "grounding chunk 同时设置多个来源", Raw: raw}
			}
			variant = index
		}
	}
	if variant < 0 {
		return GroundingChunk{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "grounding chunk 缺少来源", Raw: raw}
	}
	fields, err := rawArray(values[variant], fmt.Sprintf("%s[%d]", path, variant), evidence)
	if err != nil {
		return GroundingChunk{}, withMethod(err, "GenerateContent")
	}
	chunk := GroundingChunk{Source: []string{"web", "retrieved_context", "maps"}[variant]}
	stringFields := []*string{&chunk.URI, &chunk.Title, &chunk.Text, &chunk.PlaceID}
	for index := 0; index < len(stringFields) && index < len(fields); index++ {
		if isJSONNull(fields[index]) {
			continue
		}
		value, err := rawString(fields[index], fmt.Sprintf("%s[%d][%d]", path, variant, index), evidence)
		if err != nil {
			return GroundingChunk{}, withMethod(err, "GenerateContent")
		}
		*stringFields[index] = value
	}
	return chunk, nil
}

func decodeGroundingSupports(raw json.RawMessage, path string, evidence json.RawMessage) ([]GroundingSupport, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	supports := make([]GroundingSupport, 0, len(values))
	for index, supportRaw := range values {
		supportPath := fmt.Sprintf("%s[%d]", path, index)
		fields, err := rawArray(supportRaw, supportPath, evidence)
		if err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
		support := GroundingSupport{}
		if segmentRaw := rawAt(fields, 0); !isJSONNull(segmentRaw) {
			support.Segment, err = decodeGroundingSegment(segmentRaw, supportPath+"[0]", evidence)
			if err != nil {
				return nil, err
			}
		}
		if indicesRaw := rawAt(fields, 1); !isJSONNull(indicesRaw) {
			indices, err := rawArray(indicesRaw, supportPath+"[1]", evidence)
			if err != nil {
				return nil, withMethod(err, "GenerateContent")
			}
			support.ChunkIndices = make([]int, 0, len(indices))
			for valueIndex, indexRaw := range indices {
				value, err := rawInt64(indexRaw, fmt.Sprintf("%s[1][%d]", supportPath, valueIndex), evidence)
				if err != nil {
					return nil, withMethod(err, "GenerateContent")
				}
				support.ChunkIndices = append(support.ChunkIndices, int(value))
			}
		}
		if scoresRaw := rawAt(fields, 2); !isJSONNull(scoresRaw) {
			scores, err := rawArray(scoresRaw, supportPath+"[2]", evidence)
			if err != nil {
				return nil, withMethod(err, "GenerateContent")
			}
			support.ConfidenceScores = make([]float64, 0, len(scores))
			for valueIndex, scoreRaw := range scores {
				score, err := rawFloat64(scoreRaw, fmt.Sprintf("%s[2][%d]", supportPath, valueIndex), evidence)
				if err != nil {
					return nil, withMethod(err, "GenerateContent")
				}
				support.ConfidenceScores = append(support.ConfidenceScores, score)
			}
		}
		supports = append(supports, support)
	}
	return supports, nil
}

func decodeGroundingSegment(raw json.RawMessage, path string, evidence json.RawMessage) (GroundingSegment, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return GroundingSegment{}, withMethod(err, "GenerateContent")
	}
	segment := GroundingSegment{}
	integerFields := []*int{&segment.PartIndex, &segment.StartIndex, &segment.EndIndex}
	for index, destination := range integerFields {
		if isJSONNull(rawAt(values, index)) {
			continue
		}
		value, err := rawInt64(values[index], fmt.Sprintf("%s[%d]", path, index), evidence)
		if err != nil {
			return GroundingSegment{}, withMethod(err, "GenerateContent")
		}
		*destination = int(value)
	}
	if textRaw := rawAt(values, 3); !isJSONNull(textRaw) {
		segment.Text, err = rawString(textRaw, path+"[3]", evidence)
		if err != nil {
			return GroundingSegment{}, withMethod(err, "GenerateContent")
		}
	}
	return segment, nil
}
