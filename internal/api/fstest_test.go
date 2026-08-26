package api

import (
	"io/fs"
	"testing/fstest"
)

// fstest is a tiny in-memory fs.FS for SPA tests.
type fstestMap = map[string]string

func newFS(m fstestMap) fs.FS {
	out := fstest.MapFS{}
	for k, v := range m {
		out[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return out
}
