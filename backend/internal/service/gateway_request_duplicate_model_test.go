package service

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

func TestParseGatewayRequestRejectsDuplicateTopLevelModel(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate":       `{"model":"cheap-model","input":"hello","model":"expensive-model"}`,
		"case-variant":    `{"model":"cheap-model","Model":"expensive-model"}`,
		"duplicate-first": `{"model":"a","model":"b","input":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), domain.PlatformAnthropic)
			if !errors.Is(err, ErrDuplicateModelField) {
				t.Fatalf("err = %v, want ErrDuplicateModelField", err)
			}
		})
	}
}

func TestParseGatewayRequestKeepsSingleModel(t *testing.T) {
	for name, body := range map[string]string{
		"plain":                     `{"model":"only-model","input":"hello"}`,
		"nested-model-not-toplevel": `{"input":{"model":"nested"},"model":"only-model"}`,
	} {
		t.Run(name, func(t *testing.T) {
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), domain.PlatformAnthropic)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if parsed.Model != "only-model" {
				t.Fatalf("model = %q, want only-model", parsed.Model)
			}
		})
	}
}
