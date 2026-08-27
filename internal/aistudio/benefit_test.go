package aistudio

import (
	"errors"
	"strings"
	"testing"
)

func TestProtocolResponseShapes(t *testing.T) {
	t.Run("benefit tier", func(t *testing.T) {
		cases := []struct {
			name      string
			raw       string
			want      BenefitTier
			wantError bool
		}{
			{name: "omitted", raw: "[]", want: BenefitTierFree},
			{name: "null", raw: "[null]", want: BenefitTierFree},
			{name: "free", raw: "[0]", want: BenefitTierFree},
			{name: "pro", raw: "[1]", want: BenefitTierPro},
			{name: "ultra", raw: "[2]", want: BenefitTierUltra},
			{name: "plus", raw: "[3]", want: BenefitTierPlus},
			{name: "trailing fields", raw: "[1,null,[7]]", want: BenefitTierPro},
			{name: "wrong root", raw: "{}", wantError: true},
			{name: "wrong type", raw: "[{}]", wantError: true},
			{name: "negative enum", raw: "[-1]", wantError: true},
			{name: "unknown enum", raw: "[4]", wantError: true},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				got, err := decodeBenefitTier([]byte(test.raw))
				if test.wantError {
					var protocolError *ProtocolEvidenceError
					if !errors.As(err, &protocolError) {
						t.Fatalf("decodeBenefitTier(%s) error = %v, want ProtocolEvidenceError", test.raw, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("decodeBenefitTier(%s) error = %v", test.raw, err)
				}
				if got != test.want {
					t.Fatalf("decodeBenefitTier(%s) = %v, want %v", test.raw, got, test.want)
				}
			})
		}
	})
	t.Run("model catalog trailing fields", func(t *testing.T) {
		catalog, err := parseModelCatalog(strings.NewReader("[[],null,[7]]"))
		if err != nil {
			t.Fatalf("parseModelCatalog() error = %v", err)
		}
		if len(catalog.models) != 0 || len(catalog.entries) != 0 {
			t.Fatalf("parseModelCatalog() = %+v, want empty catalog", catalog)
		}
	})
}
