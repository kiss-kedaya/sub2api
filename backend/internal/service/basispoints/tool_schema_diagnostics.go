package basispoints

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ToolSchemaValidationError contains bounded diagnostic metadata only. Never
// retain or wrap the compiler error: it can contain URLs and private schema data.
// Path identifies the catalog entry followed by a JSON Pointer into its schema.
type ToolSchemaValidationError struct {
	Tool   string
	Path   string
	Reason string
}

func (e *ToolSchemaValidationError) Error() string {
	return fmt.Sprintf("basispoints tool schema rejected: %s (tool=%q; path=%s)", e.Reason, e.Tool, e.Path)
}

func contextualToolSchemaError(name, path string, err error) error {
	if len(path) > 1024 {
		path = path[:512] + "[truncated]" + path[len(path)-512:]
	}
	diagnostic := &ToolSchemaValidationError{Tool: safeSchemaName(name), Path: path, Reason: "resource_limit"}
	if schemaErr, ok := err.(*ToolSchemaValidationError); ok {
		diagnostic.Reason = schemaErr.Reason
		if schemaErr.Path != "" {
			diagnostic.Path += "#" + schemaErr.Path
		}
	}
	return diagnostic
}

func safeSchemaName(value string) string {
	// Names are identifiers, not arbitrary strings. Reject URL/query/control
	// characters rather than quoting potentially sensitive caller text.
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', strings.ContainsRune("_.-$~/", char):
			continue
		default:
			return "[redacted]"
		}
	}
	if len(value) > 128 {
		return "[redacted]"
	}
	return value
}

func schemaPointer(path []string, safe bool) string {
	var pointer strings.Builder
	for _, part := range path {
		if safe {
			part = safeSchemaName(part)
			if pointer.Len()+len(part) > 768 {
				_, _ = pointer.WriteString("/[truncated]")
				break
			}
		}
		_ = pointer.WriteByte('/')
		_, _ = pointer.WriteString(strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1"))
	}
	return pointer.String()
}

// Classification uses library types, never compiler error strings. Resolution
// below is diagnostic-only and runs after rejection; the compiler remains the
// sole authority on acceptance, dialects, IDs, anchors and resource limits.
func diagnoseToolSchema(parameters object, err error) *ToolSchemaValidationError {
	result := &ToolSchemaValidationError{Reason: "invalid_schema"}
	var target, location string
	var referenceError bool
	switch failure := err.(type) {
	case *jsonschema.LoadURLError:
		result.Reason, target, referenceError = "external_reference_blocked", failure.URL, true
	case *jsonschema.JSONPointerNotFoundError:
		result.Reason, target, referenceError = "local_reference_not_found", failure.URL, true
	case *jsonschema.AnchorNotFoundError:
		result.Reason, target, referenceError = "local_reference_not_found", failure.Reference, true
	case *jsonschema.InvalidJsonPointerError:
		target, referenceError = failure.URL, true
	case *jsonschema.SchemaValidationError:
		location = failure.URL
		if validation, ok := failure.Err.(*jsonschema.ValidationError); ok {
			// Choose a leaf without rendering ErrorKind, which may echo values.
			for len(validation.Causes) > 0 {
				validation = validation.Causes[0]
			}
			result.Path = schemaPointer(validation.InstanceLocation, true)
		}
	case *jsonschema.InvalidRegexError:
		location = failure.URL
	}
	if location != "" {
		if parsed, parseErr := url.Parse(location); parseErr == nil {
			result.Path = safeSchemaFragment(parsed.Fragment) + result.Path
		}
	}
	if referenceError {
		result.Path = locateToolSchemaReference(parameters, target, result.Reason)
	}
	return result
}

func safeSchemaFragment(fragment string) string {
	if fragment == "" || !strings.HasPrefix(fragment, "/") {
		return ""
	}
	parts := strings.Split(fragment[1:], "/")
	for index, part := range parts {
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return schemaPointer(parts, true)
}

type schemaReferenceLocation struct {
	target *url.URL
	path   []string
}

func locateToolSchemaReference(parameters object, target, reason string) string {
	base, err := url.Parse(toolSchemaURL)
	if err != nil {
		return ""
	}
	resources := map[string]string{toolSchemaURL: ""}
	var references []schemaReferenceLocation
	var visit func(object, []string, *url.URL, int)
	visit = func(node object, path []string, base *url.URL, draft int) {
		// The existing schema budget already bounds depth at 128. Keep a
		// separate guard so this diagnostic cannot exceed that boundary.
		if len(path) > 128 {
			return
		}
		if declared := diagnosticSchemaDraft(text(node["$schema"])); declared != 0 {
			idKey := "$id"
			if declared == 4 {
				idKey = "id"
			}
			if len(path) == 0 || text(node[idKey]) != "" {
				draft = declared
			}
		}
		idKey := "$id"
		if draft == 4 {
			idKey = "id"
		}
		// Older drafts ignore all siblings of $ref, including id.
		_, hasRef := node["$ref"]
		if draft < 2019 && hasRef {
			if relative, err := url.Parse(text(node["$ref"])); err == nil {
				references = append(references, schemaReferenceLocation{base.ResolveReference(relative), appendSchemaPath(path, "$ref")})
			}
			return
		}
		if id := text(node[idKey]); id != "" {
			if relative, err := url.Parse(id); err == nil {
				resolved := base.ResolveReference(relative)
				resolved.Fragment, resolved.RawFragment = "", ""
				base = resolved
				resources[base.String()] = schemaPointer(path, false)
			}
		}
		keywords := []string{"$ref", "$schema"}
		if draft >= 2020 {
			keywords = append(keywords, "$dynamicRef")
		} else if draft == 2019 {
			keywords = append(keywords, "$recursiveRef")
		}
		for _, keyword := range keywords {
			if reference, ok := node[keyword].(string); ok {
				if relative, err := url.Parse(reference); err == nil {
					references = append(references, schemaReferenceLocation{base.ResolveReference(relative), appendSchemaPath(path, keyword)})
				}
			}
		}
		// Visit schema-bearing keywords only. A $ref in const/default/enum or
		// an example is data and must never be reported as a failing reference.
		for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies"} {
			if draft < 2019 && (keyword == "$defs" || keyword == "dependentSchemas") {
				continue
			}
			children, _ := node[keyword].(object)
			keys := make([]string, 0, len(children))
			for key := range children {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if child, ok := children[key].(object); ok {
					visit(child, appendSchemaPath(path, keyword, key), base, draft)
				}
			}
		}
		for _, keyword := range []string{"not", "if", "then", "else", "allOf", "anyOf", "oneOf", "items", "prefixItems", "additionalItems", "additionalProperties", "propertyNames", "contains", "unevaluatedItems", "unevaluatedProperties", "contentSchema"} {
			if draft < 2020 && keyword == "prefixItems" || draft < 2019 && (keyword == "unevaluatedItems" || keyword == "unevaluatedProperties" || keyword == "contentSchema") || draft < 7 && (keyword == "if" || keyword == "then" || keyword == "else") || draft < 6 && (keyword == "contains" || keyword == "propertyNames") {
				continue
			}
			switch child := node[keyword].(type) {
			case object:
				visit(child, appendSchemaPath(path, keyword), base, draft)
			case []any:
				for index, raw := range child {
					if item, ok := raw.(object); ok {
						visit(item, appendSchemaPath(path, keyword, strconv.Itoa(index)), base, draft)
					}
				}
			}
		}
	}
	visit(parameters, nil, base, 2020)
	var matches [][]string
	for _, reference := range references {
		resolved := *reference.target
		fragment := resolved.Fragment
		resolved.Fragment, resolved.RawFragment = "", ""
		match := reference.target.String() == target
		if reason == "external_reference_blocked" {
			match = resolved.String() == target
		} else if resourcePath, local := resources[resolved.String()]; local && strings.HasPrefix(fragment, "/") {
			// Missing JSON pointers are reported relative to the physical root,
			// including the prefix of an embedded $id resource.
			physical := *base
			physical.Fragment = resourcePath + fragment
			match = match || physical.String() == target
		}
		if match {
			matches = append(matches, reference.path)
		}
	}
	// Ambiguous or unfamiliar dialects retain the exact tool/schema location;
	// never guess which of several references caused the compiler failure.
	if len(matches) == 1 {
		return schemaPointer(matches[0], true)
	}
	return ""
}

func appendSchemaPath(path []string, parts ...string) []string {
	result := make([]string, 0, len(path)+len(parts))
	result = append(result, path...)
	return append(result, parts...)
}

// Unknown/custom dialects never change acceptance and can fall back to the
// catalog/schema location when their reference cannot be identified uniquely.
func diagnosticSchemaDraft(dialect string) int {
	switch strings.TrimSuffix(dialect, "#") {
	case "http://json-schema.org/draft-04/schema", "https://json-schema.org/draft-04/schema":
		return 4
	case "http://json-schema.org/draft-06/schema", "https://json-schema.org/draft-06/schema":
		return 6
	case "http://json-schema.org/draft-07/schema", "https://json-schema.org/draft-07/schema":
		return 7
	case "https://json-schema.org/draft/2019-09/schema", "http://json-schema.org/draft/2019-09/schema":
		return 2019
	case "https://json-schema.org/draft/2020-12/schema", "http://json-schema.org/draft/2020-12/schema":
		return 2020
	default:
		return 0
	}
}
