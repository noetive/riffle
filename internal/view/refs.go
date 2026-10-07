package view

import (
	"strconv"
	"strings"
)

// refTable hands out short refs per kind (b a f d r) and keeps them stable
// for a backend id across consecutive views.
type refTable struct {
	previous map[int64]string
	counter  map[string]int
	byID     map[int64]string
	refs     map[string]int64
}

func newRefTable(prev map[string]int64) *refTable {
	r := &refTable{
		previous: map[int64]string{},
		counter:  map[string]int{},
		byID:     map[int64]string{},
		refs:     map[string]int64{},
	}
	for ref, id := range prev {
		r.previous[id] = ref
		kind := strings.TrimRight(ref, "0123456789")
		if n, err := strconv.Atoi(ref[len(kind):]); err == nil && n > r.counter[kind] {
			r.counter[kind] = n
		}
	}
	return r
}

// get returns the ref of a backend id, allocating one of the given kind when
// the node has none yet. A previous ref is reused only when its kind still
// matches; numbers of vanished nodes are never handed to other nodes.
func (r *refTable) get(id int64, kind string) string {
	if ref, ok := r.byID[id]; ok {
		return ref
	}
	ref, ok := r.previous[id]
	if !ok || strings.TrimRight(ref, "0123456789") != kind {
		r.counter[kind]++
		ref = kind + strconv.Itoa(r.counter[kind])
	}
	r.byID[id] = ref
	r.refs[ref] = id
	return ref
}

// lookup returns the ref already assigned to a backend id.
func (r *refTable) lookup(id int64) (string, bool) {
	ref, ok := r.byID[id]
	return ref, ok
}

// resolve finds the backend id behind a ref, in this compilation first and
// then in the previous view.
func (r *refTable) resolve(ref string) (int64, bool) {
	if id, ok := r.refs[ref]; ok {
		return id, true
	}
	for id, old := range r.previous {
		if old == ref {
			return id, true
		}
	}
	return 0, false
}

func (r *refTable) table() map[string]int64 {
	out := make(map[string]int64, len(r.refs))
	for k, v := range r.refs {
		out[k] = v
	}
	return out
}

// discard takes back a ref handed out for a backend id that ended up unused.
// Its number is not reused: numbers of refs never shown stay spent.
func (r *refTable) discard(id int64) {
	if ref, ok := r.byID[id]; ok {
		delete(r.byID, id)
		delete(r.refs, ref)
	}
}
