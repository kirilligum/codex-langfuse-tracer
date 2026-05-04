package agenttrace

import "fmt"

func StringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func StringOr(value any, defaultValue string) string {
	if text := StringValue(value); text != "" {
		return text
	}
	return defaultValue
}

func MapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func SliceValue(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}

func IntValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case jsonNumber:
		result, _ := typed.Int64()
		return int(result)
	default:
		return 0
	}
}

type jsonNumber interface {
	Int64() (int64, error)
}
