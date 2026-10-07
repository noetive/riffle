package session

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

// fingerprint is what stays true of a node when a framework remounts it:
// what it is, what it is called, its stable attributes and where it sits.
type fingerprint struct {
	role, name, tag, attrs, pos string
}

// stableAttrs are attributes that identify a node across remounts.
var stableAttrs = []string{"id", "name", "type", "href", "placeholder", "data-testid", "aria-label"}

func (c *scene) fingerprintOf(i int32) fingerprint {
	snap := c.page.Snap
	var attrs []string
	for _, a := range stableAttrs {
		if v, ok := snap.Attr(i, a); ok {
			attrs = append(attrs, a+"="+v)
		}
	}
	return fingerprint{
		role:  c.page.Nodes[i].Role,
		name:  c.page.Nodes[i].Name,
		tag:   snap.Tag[i],
		attrs: strings.Join(attrs, "|"),
		pos:   c.position(i),
	}
}

// position is the node's place among its ancestors: tag and index among
// same-tag siblings, for the node and up to four ancestors.
func (c *scene) position(i int32) string {
	snap := c.page.Snap
	var parts []string
	for n, depth := i, 0; n != snapshot.None && depth < 5; n, depth = snap.Parent[n], depth+1 {
		idx := 0
		if p := snap.Parent[n]; p != snapshot.None {
			for _, sib := range snap.Children[p] {
				if sib == n {
					break
				}
				if snap.Tag[sib] == snap.Tag[n] {
					idx++
				}
			}
		}
		parts = append(parts, snap.Tag[n]+":"+strconv.Itoa(idx))
	}
	return strings.Join(parts, "/")
}

// rebind finds the node a ref meant after its engine identity is gone. It
// binds only when exactly one node fits; any doubt is stale.
func (c *scene) rebind(ref string) (int32, error) {
	want, ok := c.prints[ref]
	if !ok {
		return snapshot.None, fmt.Errorf("stale: ref %s is not known; run `view interactive`, or `view outline` for regions and progress bars, for current refs", ref)
	}
	var same, placed []int32
	for i := range c.page.Nodes {
		if c.page.Snap.Kind[i] != snapshot.KindElement {
			continue
		}
		got := c.fingerprintOf(int32(i))
		if got.role == want.role && got.name == want.name && got.tag == want.tag && got.attrs == want.attrs {
			same = append(same, int32(i))
			if got.pos == want.pos {
				placed = append(placed, int32(i))
			}
		}
	}
	pick := same
	if len(pick) > 1 {
		pick = placed
	}
	if len(pick) != 1 {
		return snapshot.None, fmt.Errorf("stale: %s was replaced and %d nodes could be it; run `view interactive`, or `view outline` for regions and progress bars, for current refs", ref, len(same))
	}
	c.refs[ref] = c.page.Snap.Backend[pick[0]]
	return pick[0], nil
}
