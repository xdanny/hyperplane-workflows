package sdkworkflow

import (
	"context"
	"testing"
)

func TestExecutionAccessRequiresExplicitBoundedGrants(t *testing.T) {
	yes, no := true, false
	base := Node{ID: "build", Kind: "task", Title: "Build", Instruction: "Create a checked file", Check: []string{"true"}}
	cases := []struct {
		name                  string
		access                *TaskAccess
		toolchain, sideEffect *bool
		kind                  string
		valid                 bool
	}{
		{"default", nil, nil, nil, "task", true},
		{"connection", &TaskAccess{Hosts: []string{"data.example.com"}, Environment: []string{"WORKFLOW_SALES_CONNECTION"}, ReadPaths: []string{"/data/input.csv"}}, nil, nil, "task", true},
		// Reading a local file is not a reason to open the network.
		{"local read", &TaskAccess{ReadPaths: []string{"/data/input.csv"}}, nil, nil, "task", true},
		{"connection only", &TaskAccess{Environment: []string{"WORKFLOW_SALES_CONNECTION"}}, nil, nil, "task", true},
		{"local read with tools", &TaskAccess{ReadPaths: []string{"/data/input.csv"}}, &yes, &yes, "task", false},
		{"tools", &TaskAccess{Hosts: []string{"pypi.org"}}, &yes, &yes, "task", true},
		{"empty hosts", &TaskAccess{}, nil, nil, "task", false},
		{"wildcard", &TaskAccess{Hosts: []string{"*.example.com"}}, nil, nil, "task", false},
		{"URL", &TaskAccess{Hosts: []string{"https://example.com"}}, nil, nil, "task", false},
		{"duplicate", &TaskAccess{Hosts: []string{"example.com", "example.com"}}, nil, nil, "task", false},
		{"root", &TaskAccess{Hosts: []string{"example.com"}, ReadPaths: []string{"/"}}, nil, nil, "task", false},
		{"traversal", &TaskAccess{Hosts: []string{"example.com"}, ReadPaths: []string{"/data/../secret"}}, nil, nil, "task", false},
		{"application env", &TaskAccess{Hosts: []string{"example.com"}, Environment: []string{"DATABASE_URL"}}, nil, nil, "task", false},
		{"no side effect grant", &TaskAccess{Hosts: []string{"pypi.org"}}, &yes, &no, "task", false},
		{"no tool setup", &TaskAccess{Hosts: []string{"pypi.org"}}, &no, &yes, "task", false},
		{"no access", nil, &yes, &yes, "task", false},
		{"document access", &TaskAccess{Hosts: []string{"example.com"}}, nil, nil, "document", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := base
			n.Access = c.access
			n.Toolchain = c.toolchain
			n.SideEffect = c.sideEffect
			n.Kind = c.kind
			if err := Validate(context.Background(), "Check data", []Node{n}); (err == nil) != c.valid {
				t.Fatalf("valid=%t error=%v", c.valid, err)
			}
		})
	}
}
