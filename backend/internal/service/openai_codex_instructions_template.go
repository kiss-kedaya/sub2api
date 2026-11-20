package service

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

type forcedCodexInstructionsTemplateData struct {
	ExistingInstructions string
	OriginalModel        string
	NormalizedModel      string
	BillingModel         string
	UpstreamModel        string
}

func applyForcedCodexInstructionsTemplate(
	reqBody map[string]any,
	templateText string,
	data forcedCodexInstructionsTemplateData,
) (bool, error) {
	rendered, err := renderForcedCodexInstructionsTemplate(templateText, data)
	if err != nil {
		return false, err
	}
	if rendered == "" {
		return false, nil
	}

	existing, _ := reqBody["instructions"].(string)
	if strings.TrimSpace(existing) == rendered {
		return false, nil
	}

	// A template with no caller guidance can be a stable gateway-owned prefix.
	// If it incorporates user guidance, keep the established turn-scoped contract.
	previous, _ := reqBody["previous_response_id"].(string)
	model, _ := reqBody["model"].(string)
	if usesGPT62CodexCachePrefix(model) && strings.TrimSpace(existing) == "" &&
		strings.TrimSpace(data.ExistingInstructions) == "" && strings.TrimSpace(previous) == "" {
		input, _ := reqBody["input"].([]any)
		if hasCodexDeveloperPrefix(input, rendered) {
			return false, nil
		}
		if prependCodexDeveloperPrefix(reqBody, rendered) {
			return true, nil
		}
	}
	reqBody["instructions"] = rendered
	return true, nil
}

func renderForcedCodexInstructionsTemplate(
	templateText string,
	data forcedCodexInstructionsTemplateData,
) (string, error) {
	tmpl, err := template.New("forced_codex_instructions").Option("missingkey=zero").Parse(templateText)
	if err != nil {
		return "", fmt.Errorf("parse forced codex instructions template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render forced codex instructions template: %w", err)
	}

	return strings.TrimSpace(buf.String()), nil
}
