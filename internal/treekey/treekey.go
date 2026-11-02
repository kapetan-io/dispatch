// Package treekey names a worker tree for the state directory's records. Two
// spellings of one tree must share records, so the path is resolved first.
package treekey

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// Of is the directory name that holds the records of tree.
func Of(tree string) string {
	return Hash(resolve(tree))
}

// Digest is the first digits hex digits of the SHA-256 of purpose, a colon and the resolved
// tree, so each purpose names one tree differently and two spellings of a tree agree.
func Digest(purpose, tree string, digits int) string {
	sum := sha256.Sum256([]byte(purpose + ":" + resolve(tree)))
	return hex.EncodeToString(sum[:])[:digits]
}

// Hash shortens s to a name that is safe to use in a path.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func resolve(tree string) string {
	if resolved, err := filepath.EvalSymlinks(tree); err == nil {
		return resolved
	}
	return tree
}
