package journey

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

const keyPrefix = "j-"
const keyHashLen = 16
const maxChainWalk = 32

func Key(project, dag, recipe, rootRunID string) string {
	identity := recipe
	if identity == "" {
		identity = "run:" + rootRunID
	}
	sum := sha256.Sum256([]byte(project + "\x00" + dag + "\x00" + identity))
	return keyPrefix + hex.EncodeToString(sum[:])[:keyHashLen]
}
func ValidKey(s string) bool {
	if !strings.HasPrefix(s, keyPrefix) || len(s) != len(keyPrefix)+keyHashLen {
		return false
	}
	_, err := hex.DecodeString(s[len(keyPrefix):])
	return err == nil
}
func chainRoot(r store.Run, byID map[string]store.Run) string {
	if root := store.SDKDeliveryRoot(r); root != r.RunID && root != store.SDKContinuationSource(r) {
		return root
	}
	cur := r
	seen := map[string]bool{cur.RunID: true}
	for i := 0; i < maxChainWalk; i++ {
		parent := store.SDKContinuationSource(cur)
		if parent == "" || seen[parent] {
			break
		}
		seen[parent] = true
		next, ok := byID[parent]
		if !ok {
			return parent
		}
		cur = next
	}
	return cur.RunID
}
