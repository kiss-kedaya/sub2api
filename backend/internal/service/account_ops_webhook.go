package service

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func accountOpsOfficialProvider(host string) string {
	switch strings.ToLower(strings.TrimSuffix(host, ".")) {
	case "qyapi.weixin.qq.com":
		return "wecom"
	case "oapi.dingtalk.com":
		return "dingtalk"
	case "open.feishu.cn":
		return "feishu"
	default:
		return ""
	}
}

func detectAccountOpsProvider(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("invalid webhook HTTPS URL")
	}
	provider := accountOpsOfficialProvider(u.Hostname())
	if provider == "" {
		provider = "custom"
	}
	if err := validateAccountOpsURL(provider, raw); err != nil {
		return "", err
	}
	return provider, nil
}

// Static checks run when saving and loading configuration. DNS addresses are
// checked again by the SSRF-safe transport at the point of connection.
func validateAccountOpsCustomURL(raw string) error {
	invalid := errors.New("webhook requires a public HTTPS URL")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") {
		return invalid
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if isBlockedHostname(host) || strings.HasSuffix(host, ".localhost") || accountOpsOfficialProvider(host) != "" || strings.ContainsAny(host, "%\\") {
		return invalid
	}
	if ip := net.ParseIP(host); ip != nil && (isPrivateIP(ip) || !ip.IsGlobalUnicast()) {
		return invalid
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return invalid
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return invalid
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return invalid
	}
	return nil
}

func opsTemplateValue(template *string) string {
	if template == nil {
		return ""
	}
	return *template
}

var opsTemplateFields = map[string]bool{
	"title": true, "message": true, "account": true,
	"balance": true, "threshold": true, "time": true,
}

func parseAccountOpsTemplate(template string) (map[string]any, error) {
	invalid := errors.New("invalid webhook JSON template")
	if len(template) > 16384 {
		return nil, invalid
	}
	if template == "" {
		template = `{"text":"{{message}}"}`
	}
	decoder := json.NewDecoder(strings.NewReader(template))
	decoder.UseNumber()
	var object map[string]any
	if decoder.Decode(&object) != nil || object == nil {
		return nil, invalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, invalid
	}
	var validate func(any, int) error
	validate = func(value any, depth int) error {
		if depth > 64 {
			return invalid
		}
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if strings.Contains(key, "{{") || strings.Contains(key, "}}") {
					return invalid
				}
				if err := validate(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := validate(child, depth+1); err != nil {
					return err
				}
			}
		case string:
			for {
				start := strings.Index(v, "{{")
				if start < 0 {
					if strings.Contains(v, "}}") {
						return invalid
					}
					break
				}
				if strings.Contains(v[:start], "}}") {
					return invalid
				}
				v = v[start+2:]
				end := strings.Index(v, "}}")
				if end < 0 || !opsTemplateFields[v[:end]] {
					return invalid
				}
				v = v[end+2:]
			}
		}
		return nil
	}
	if err := validate(object, 0); err != nil {
		return nil, err
	}
	return object, nil
}

func accountOpsCustomPayload(template string, message accountOpsMessage) (map[string]any, error) {
	object, err := parseAccountOpsTemplate(template)
	if err != nil {
		return nil, err
	}
	title := message.robotTitle
	if title == "" {
		title = message.title
	}
	replace := strings.NewReplacer(
		"{{title}}", title, "{{message}}", title+"\n"+message.plain,
		"{{account}}", message.account, "{{balance}}", message.balance,
		"{{threshold}}", message.threshold, "{{time}}", message.observedTime,
	)
	var render func(any) any
	render = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				v[key] = render(child)
			}
		case []any:
			for i, child := range v {
				v[i] = render(child)
			}
		case string:
			return replace.Replace(v)
		}
		return value
	}
	render(object)
	return object, nil
}
