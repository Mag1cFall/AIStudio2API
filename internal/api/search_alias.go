package api

import (
	"context"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// generate resolves provider-independent model aliases at the shared API
// boundary before account routing and model capability validation.
func (s *server) generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	request, _ = aistudio.ResolveSearchGenerateRequest(request)
	return s.service.Generate(ctx, request)
}

func resolveSearchTokenCountRequest(request aistudio.TokenCountRequest) aistudio.TokenCountRequest {
	generate, enabled := aistudio.ResolveSearchGenerateRequest(aistudio.GenerateRequest{
		Model: request.Model,
		Tools: request.Tools,
	})
	if enabled {
		request.Model = generate.Model
		request.Tools = generate.Tools
	}
	return request
}
