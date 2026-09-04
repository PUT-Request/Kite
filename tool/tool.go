package tool

import (
	"context"

	"kite/llm"
)

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error)
}

func ToLLMTool(t Tool) llm.ToolDef {
	return llm.ToolDef{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}

func strParam(desc string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"description": desc,
	}
}

func intParam(desc string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "integer",
		"description": desc,
	}
}

func strEnumParam(desc string, enum []string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"description": desc,
		"enum":        enum,
	}
}

func params(props map[string]interface{}, required []string) map[string]interface{} {
	p := map[string]interface{}{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		p["required"] = required
	}
	return p
}

func StrParam(desc string) map[string]interface{} {
	return strParam(desc)
}

func IntParam(desc string) map[string]interface{} {
	return intParam(desc)
}

func StrEnumParam(desc string, enum []string) map[string]interface{} {
	return strEnumParam(desc, enum)
}

func ObjectParams(props map[string]interface{}, required []string) map[string]interface{} {
	return params(props, required)
}

func getStringArg(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getIntArg(args map[string]interface{}, key string, defaultVal int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return defaultVal
}
