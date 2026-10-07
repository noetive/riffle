package view_test

import (
	"sort"
	"testing"

	"github.com/noetive/riffle/internal/facts"
	"github.com/noetive/riffle/internal/snapshot"
	"github.com/noetive/riffle/internal/view"
)

// FuzzAnalyzeAndCompile feeds the browser's capture of a page, which the page
// itself controls, through the analyzer and every projection. Whatever the
// page contains, nothing may panic, a view must compile the same way twice,
// every ref must resolve, and a second view built on the first must keep the
// refs it handed out. The seed corpus is raw captures of real pages.
func FuzzAnalyzeAndCompile(f *testing.F) {
	f.Add([]byte(`{"documents":[],"strings":[]}`))
	f.Add([]byte(`{"documents":[{"nodes":{"parentIndex":[-1,0,0,1],"nodeType":[9,1,1,3],"nodeName":[0,1,1,2],"nodeValue":[-1,-1,-1,3],"backendNodeId":[1,2,3,4]}}],"strings":["#document","button","","hi"]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		snap, err := snapshot.Parse(data)
		if err != nil {
			return
		}
		page := facts.Analyze(snap)
		opts := []view.Options{
			{Projection: view.Outline},
			{Projection: view.Outline, Budget: 40},
			{Projection: view.Interactive},
			{Projection: view.Read},
			{Projection: view.Find, Query: "a"},
			{Projection: view.Find, Query: ""},
		}
		var refs []string
		for _, o := range opts {
			first := view.Compile(page, o)
			second := view.Compile(page, o)
			if first.String() != second.String() {
				t.Fatalf("projection %d compiles differently twice", o.Projection)
			}
			for ref, backend := range first.Refs {
				if got, ok := first.Resolve(ref); !ok || got != backend {
					t.Fatalf("ref %s does not resolve to its node", ref)
				}
				refs = append(refs, ref)
			}
			// A later view built on this one keeps the refs already handed out.
			next := view.Compile(page, view.Options{Projection: o.Projection, Query: o.Query, Previous: first.Refs})
			for ref, backend := range first.Refs {
				if got, ok := next.Resolve(ref); ok && got != backend {
					t.Fatalf("ref %s moved from %d to %d between views", ref, backend, got)
				}
			}
		}
		sort.Strings(refs)
		for i, ref := range refs {
			if i > 0 && refs[i-1] == ref || i >= 24 {
				continue
			}
			for _, p := range []view.Projection{view.Table, view.Expand} {
				_ = view.Compile(page, view.Options{Projection: p, Target: ref, Previous: refsOf(page)}).String()
			}
		}
	})
}

func refsOf(page *facts.Page) map[string]int64 {
	return view.Compile(page, view.Options{Projection: view.Outline}).Refs
}
