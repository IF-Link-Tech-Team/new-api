package claude

// FunctionParametersToInputSchema preserves the client's schema without adding
// null properties/required fields when optional JSON Schema keywords are absent.
func FunctionParametersToInputSchema(parameters any) map[string]any {
	schema := make(map[string]any)
	if params, ok := parameters.(map[string]any); ok {
		for key, value := range params {
			schema[key] = value
		}
	}
	if schema["type"] == nil {
		schema["type"] = "object"
	}
	if schema["properties"] == nil {
		schema["properties"] = map[string]any{}
	}
	return schema
}
