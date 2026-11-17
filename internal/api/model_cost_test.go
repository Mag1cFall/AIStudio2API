package api

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/pricing"
)

func TestModelEstimatedCost(t *testing.T) {
	for _, id := range []string{"gemini-4-hydrogen", "gemini-3.1-pro-preview", "gemini-decision-1"} {
		for _, suffix := range []string{"", "-search"} {
			t.Run(id+suffix, func(t *testing.T) {
				body, err := json.Marshal(openAIModelObject(aistudio.Model{ID: id + suffix}))
				if err != nil {
					t.Fatal(err)
				}
				type rate struct {
					Input, Output float64
					CacheRead     float64 `json:"cache_read"`
				}
				var model struct {
					Cost *struct {
						rate
						Tiers []struct {
							rate
							Above int
						}
					} `json:"estimated_cost"`
				}
				if err := json.Unmarshal(body, &model); err != nil {
					t.Fatal(err)
				}
				if model.Cost == nil {
					t.Fatalf("%s has no estimated_cost", body)
				}
				for _, input := range []int{200000, 200001, 1000000} {
					r := model.Cost.rate
					for _, tier := range model.Cost.Tiers {
						if input > tier.Above {
							r = tier.rate
						}
					}
					cached, output := 100000, 2500
					got := (float64(input-cached)*r.Input + float64(cached)*r.CacheRead + float64(output)*r.Output) / 1e6
					want := pricing.Cost(id, int64(input), int64(cached), int64(output))
					if math.Abs(got-want) > 1e-12 {
						t.Fatalf("input=%d: estimated_cost=%g, usage=%g", input, got, want)
					}
				}
			})
		}
	}
	for _, id := range []string{"unknown", "unknown-search", "gemini-4-hydrogen-build", "gemini-2.5-flash-image"} {
		if _, ok := openAIModelObject(aistudio.Model{ID: id})["estimated_cost"]; ok {
			t.Fatalf("%s should be unpriced", id)
		}
	}
}
