package snapshot_test

import (
	"testing"

	"github.com/noetive/riffle/internal/snapshot"
)

// FuzzParse checks that Parse never panics on arbitrary bytes. Returning an
// error is a valid outcome, and so is a snapshot built from partial data.
//
// The corpus entry testdata/fuzz/FuzzParse/ParallelArraysShorterThanParentIndex
// reproduces a reported panic: nodes.nodeType and its sibling arrays are
// indexed by the length of parentIndex without a bounds check.
func FuzzParse(f *testing.F) {
	f.Add([]byte(capture))
	f.Add([]byte(`{"documents":[]}`))
	f.Add([]byte(`{"documents":[{"nodes":{"parentIndex":[-1,0],"nodeType":[9,1],"nodeName":[0,0],"nodeValue":[-1,-1],"backendNodeId":[1,2],"attributes":[[],[0,1]]},"layout":{"nodeIndex":[9],"bounds":[[1]],"styles":[[3]]}}],"strings":["a"]}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := snapshot.Parse(data)
		if err == nil && s == nil {
			t.Fatal("nil snapshot without an error")
		}
	})
}
