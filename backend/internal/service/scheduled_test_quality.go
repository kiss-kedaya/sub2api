package service

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// Transport success and quality approval are separate outcomes. An arbitrary
// custom prompt has no local quality rubric and must not change account state.
func assessScheduledTestQuality(response, prompt string) (string, string) {
	if strings.TrimSpace(prompt) != "" && strings.TrimSpace(prompt) != DefaultScheduledTestPrompt {
		return "unknown", "quality check inconclusive: custom prompt has no quality evaluator"
	}
	if len(response) > 1024*1024 {
		return "unknown", "quality check inconclusive: document exceeds local analysis limit"
	}
	if reason := scheduledTestQualityFailure(response); reason != "" {
		return "degraded", reason
	}
	return scheduledTestAnimationQuality(response)
}

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
	if reason := scheduledTestDocumentFailure(content); reason != "" {
		return reason
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

func scheduledTestDocumentFailure(content string) string {
	// The tokenizer ignores fake closing tags inside comments and scripts.
	z := html.NewTokenizer(strings.NewReader(content))
	htmlDepth, svgDepth, htmlCount, svgCount := 0, 0, 0, 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF {
				return "quality check failed: invalid HTML document"
			}
			break
		}
		if tt != html.StartTagToken && tt != html.EndTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		token := z.Token()
		switch token.Data {
		case "html":
			switch tt {
			case html.StartTagToken:
				htmlDepth++
				htmlCount++
			case html.EndTagToken:
				htmlDepth--
			}
		case "svg":
			switch tt {
			case html.StartTagToken:
				svgDepth++
				svgCount++
			case html.EndTagToken:
				svgDepth--
			}
		}
		if htmlDepth < 0 || svgDepth < 0 {
			return "quality check failed: unbalanced HTML/SVG document"
		}
		if tt == html.EndTagToken {
			continue
		}
		for _, attr := range token.Attr {
			value := strings.ToLower(strings.TrimSpace(attr.Val))
			if (token.Data == "script" && attr.Key == "src") ||
				(token.Data == "link" && attr.Key == "href") ||
				((attr.Key == "href" || attr.Key == "src") && (strings.HasPrefix(value, "http:") || strings.HasPrefix(value, "https:") || strings.HasPrefix(value, "//"))) {
				return "quality check failed: external resource dependency detected"
			}
		}
	}
	if htmlDepth != 0 || svgDepth != 0 || svgCount == 0 || htmlCount != 1 {
		return "quality check failed: unbalanced HTML/SVG document"
	}
	return ""
}
