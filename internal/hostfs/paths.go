// Package hostfs holds the paths value, every path the tool touches.
package hostfs

// Paths is every path the tool touches on the Host. Its fields (a prefix for tests and the Data root) arrive
// with the path methods.
type Paths struct{}

// System returns the real fixed paths, with no Data root yet.
func System() Paths { return Paths{} }
