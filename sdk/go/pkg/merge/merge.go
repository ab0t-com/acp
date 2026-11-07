// Package merge is the public face of the line merges a client needs. Union joins a
// shared text and a local text when there is no common ancestor to merge against.
package merge

import "github.com/ab0t-com/acp/sdk/go/internal/merge"

// Union returns a text that holds both inputs: each is a subsequence of the result, so
// turning shared into it deletes nothing, and merging the result again adds no line.
func Union(shared, local string) string { return merge.Union(shared, local) }
