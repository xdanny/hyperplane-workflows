package store

import (
	"context"
	"strings"
)

// RecipeNamer resolves SDK template IDs to readable names once per request.
// Unknown or deleted templates leave the caller's fallback unchanged.
type RecipeNamer struct {
	byID map[string]string
}

func (s *Store) NewRecipeNamer(ctx context.Context) (*RecipeNamer, error) {
	n := &RecipeNamer{byID: map[string]string{}}
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return n, err
	}
	for _, t := range templates {
		if strings.TrimSpace(t.Name) != "" {
			n.byID[t.ID] = displayRecipeName(t.Name)
		}
	}
	return n, nil
}

// Name is the name to print for a template id, or "" when the id is unknown —
// a deleted template, or a doc that stamped something that never was one.
func (n *RecipeNamer) Name(id string) string {
	if n == nil || n.byID == nil {
		return ""
	}
	return n.byID[strings.TrimSpace(id)]
}

// displayRecipeName presents an unfamiliar template slug as a readable name.
func displayRecipeName(name string) string {
	n := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(name, "-", " "), "_", " "))
	if n == "" {
		return "This recipe"
	}
	return strings.ToUpper(n[:1]) + n[1:]
}
