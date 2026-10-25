//go:build !bundled

package bundle

import "io/fs"

const Available = false

var Weights, Vocabulary []byte
var payload fs.FS

func preload([]string) error { return nil }
