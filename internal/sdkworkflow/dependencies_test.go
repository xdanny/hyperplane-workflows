package sdkworkflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDependencyJSONPreservesIndependentRoots(t *testing.T) {
	for _, scenario := range []struct{ input, want string }{
		{`{"id":"a"}`, ``},
		{`{"id":"a","dependsOn":null}`, ``},
		{`{"id":"a","dependsOn":[]}`, `"dependsOn":[]`},
		{`{"id":"a","dependsOn":["b","c"]}`, `"dependsOn":["b","c"]`},
	} {
		var node Node
		if err := json.Unmarshal([]byte(scenario.input), &node); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(node)
		if err != nil {
			t.Fatal(err)
		}
		if scenario.want == "" {
			if strings.Contains(string(data), "dependsOn") {
				t.Fatalf("omitted dependency changed: %s", data)
			}
		} else if !strings.Contains(string(data), scenario.want) {
			t.Fatalf("dependency lost: %s", data)
		}
	}
}
