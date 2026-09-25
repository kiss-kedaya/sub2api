package service

import "strings"

func scheduledTestQualityFailure(response string) string {
	content := strings.TrimSpace(strings.TrimPrefix(response, "\ufeff"))
	if content == "" {
		return "quality check failed: empty response"
	}
	if strings.Contains(content, "```") {
		return "quality check failed: response contains a code fence"
	}
	if !strings.HasPrefix(content, "<") || !strings.HasSuffix(content, ">") {
		return "quality check failed: response contains text outside the HTML document"
	}
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "<html") || !strings.Contains(lower, "<svg") || !strings.Contains(lower, "</html>") {
		return "quality check failed: response is not a complete HTML/SVG document"
	}
	if !strings.Contains(lower, "</svg>") {
		return "quality check failed: SVG document is incomplete"
	}
	if !strings.Contains(lower, "<animate") &&
		!strings.Contains(lower, "@keyframes") &&
		!strings.Contains(lower, "animation:") &&
		!strings.Contains(lower, "animation-name") &&
		!strings.Contains(lower, "requestanimationframe") {
		return "quality check failed: no animation implementation detected"
	}
	if strings.Contains(lower, "<script src=") ||
		strings.Contains(lower, "<link ") ||
		strings.Contains(lower, "src=\"http") ||
		strings.Contains(lower, "href=\"http") ||
		strings.Contains(lower, "url(http") {
		return "quality check failed: external resource dependency detected"
	}
	return ""
}
