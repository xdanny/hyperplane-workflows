//go:build smoke

package localmodel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/localmodel"
)

func TestSmokeLive(t *testing.T) {
	ctx := context.Background()

	t.Log("=== Reachability check ===")
	ok, info := localmodel.Reachable(ctx)
	t.Logf("Reachable: %v, info: %s", ok, info)

	if !ok {
		t.Skip("Local LLM not reachable — skipping live smoke test")
	}

	t.Log("=== Chat completion ===")
	res, err := localmodel.Run(ctx, localmodel.Options{
		Instruction: "Reply with a single sentence: what is 2+2?",
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	fmt.Printf("OK:           %v\n", res.OK)
	fmt.Printf("Text:         %s\n", res.Text)
	fmt.Printf("InputTokens:  %d\n", res.InputTokens)
	fmt.Printf("OutputTokens: %d\n", res.OutputTokens)
	if res.ErrorMsg != "" {
		fmt.Printf("ErrorMsg:     %s\n", res.ErrorMsg)
	}
	if !res.OK || res.Text == "" {
		t.Errorf("expected OK=true and non-empty text")
	}
}
