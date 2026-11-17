package sdkplanning

import "encoding/json"

// Checked work selects one shipped recipe; the application expands its complete
// stages and criteria. The model only supplies the owner's slot declarations.
func proposalSchema() []byte {
	recipes := []string{""}
	for _, recipe := range checkedRecipes() {
		recipes = append(recipes, recipe.Name)
	}
	text := map[string]any{"type": "string"}
	schema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name", "nodes", "unsupported", "recipe", "slots"}, "properties": map[string]any{
		"name": text, "recipe": map[string]any{"type": "string", "enum": recipes},
		"slots":       map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name", "value"}, "properties": map[string]any{"name": text, "value": text}}},
		"nodes":       map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "title", "instruction", "kind", "dependsOn"}, "properties": map[string]any{"id": text, "dependsOn": map[string]any{"type": "array", "items": text, "maxItems": 20}, "title": text, "instruction": text, "kind": map[string]any{"type": "string", "enum": []string{"document", "research", "check", "approval", "task"}}}}},
		"unsupported": map[string]any{"type": "array", "items": text},
	}}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return raw
}
