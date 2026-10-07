package facts

import (
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

var replacedTags = map[string]bool{
	"img": true, "video": true, "canvas": true, "iframe": true, "svg": true, "embed": true, "object": true,
}

func (a *analyzer) layers() {
	a.modals()
	a.occlusion()
	a.inertBehindNativeModals()
}

func (a *analyzer) layered(i int32) bool {
	p := a.style(i)[snapshot.Position]
	return a.s.Laid[i] && (p == "fixed" || p == "absolute")
}

// large reports whether box i spans at least half of the viewport.
func (a *analyzer) large(i int32) bool {
	s := a.s
	vp := s.ViewportW * s.ViewportH
	if vp <= 0 || !s.Laid[i] {
		return false
	}
	return s.Box[i].W*s.Box[i].H >= 0.5*vp
}

// modals marks dialog layers. A dialog is modal when it declares aria-modal
// (WAI-ARIA makes that independent of how its box is placed, so a dialog in
// a fixed container is one), or when it is positioned out of flow and spans
// most of the viewport, sits on a large backdrop, or is a native dialog in
// the top layer.
func (a *analyzer) modals() {
	s := a.s
	a.backdrops = map[int32]int32{}
	for i := int32(0); i < int32(a.n); i++ {
		nd := &a.nodes[i]
		if s.Kind[i] != snapshot.KindElement || !nd.Visible() {
			continue
		}
		if nd.Role != "dialog" && nd.Role != "alertdialog" {
			continue
		}
		ariaModal := strings.EqualFold(strings.TrimSpace(a.attr(i, "aria-modal")), "true")
		if !ariaModal && !a.layered(i) {
			continue
		}
		native := s.Tag[i] == "dialog" && a.style(i)[snapshot.Position] == "fixed"
		if native {
			a.nativeModals = append(a.nativeModals, i)
		}
		// The backdrop is a large positioned wrapper above the dialog or a
		// large positioned sibling of it or of those wrappers. A dialog in the
		// page's flow has only the wrapper that lifts it out: a large layer
		// beside it is the page's own, such as a section's background.
		inFlow := !a.layered(i)
		// A dialog in the flow pushes the page aside rather than lying over it.
		covers := (a.large(i) && !inFlow) || native
		x := i
		for depth := 0; depth < 4 && x != snapshot.None; depth++ {
			if depth > 0 && a.layered(x) && a.large(x) {
				a.backdrops[x] = i
				covers = true
			}
			if p := s.Parent[x]; p != snapshot.None && !inFlow {
				for _, sib := range s.Children[p] {
					if sib != x && sib != i && s.Kind[sib] == snapshot.KindElement && a.nodes[sib].Visible() && a.layered(sib) && a.large(sib) {
						a.backdrops[sib] = i
						covers = true
					}
				}
			}
			x = s.Parent[x]
		}
		if ariaModal || covers {
			nd.Modal = true
			nd.Covers = covers
		} else {
			for k, m := range a.backdrops {
				if m == i {
					delete(a.backdrops, k)
				}
			}
		}
	}
}

// inertBehindNativeModals covers everything outside the topmost showModal()
// dialog with that dialog: the browser lets no click through its backdrop, but
// the backdrop is no node for occlusion to find.
func (a *analyzer) inertBehindNativeModals() {
	s := a.s
	top := snapshot.None
	for _, d := range a.nativeModals {
		if top == snapshot.None || s.Paint[d] > s.Paint[top] {
			top = d
		}
	}
	if top == snapshot.None {
		return
	}
	for i := int32(0); i < int32(a.n); i++ {
		if s.Kind[i] != snapshot.KindElement || !a.nodes[i].Visible() || !s.Laid[i] || s.Box[i].Empty() {
			continue
		}
		if t := s.Tag[i]; t == "html" || t == "body" {
			continue
		}
		if i == top || a.isAncestor(i, top) || a.isAncestor(top, i) || a.nodes[i].CoveredBy != snapshot.None {
			continue
		}
		a.nodes[i].CoveredBy = top
	}
}

// occlusion sets CoveredBy: every sample point (center and corners) of the
// box lies in a later-painted, pointer-receiving box of a node that is
// neither ancestor nor descendant.
func (a *analyzer) occlusion() {
	s := a.s
	var cov []int32
	for i := int32(0); i < int32(a.n); i++ {
		if s.Kind[i] != snapshot.KindElement || !a.nodes[i].Visible() || !s.Laid[i] || s.Box[i].Empty() {
			continue
		}
		st := &s.Style[i]
		bg, ok := parseColor(st[snapshot.BackgroundColor])
		a.nodes[i].Painted = (ok && bg.a >= 0.05) || replacedTags[s.Tag[i]]
		if a.nodes[i].Painted && st[snapshot.PointerEvents] != "none" && !a.nodes[i].Holds {
			cov = append(cov, i)
		}
	}
	if len(cov) == 0 {
		return
	}
	for i := int32(0); i < int32(a.n); i++ {
		if s.Kind[i] != snapshot.KindElement || !a.nodes[i].Visible() || !s.Laid[i] || s.Box[i].Empty() {
			continue
		}
		if t := s.Tag[i]; t == "html" || t == "body" {
			continue
		}
		b := s.Box[i]
		cx, cy := b.Center()
		best := snapshot.None
		hit := func(j int32, x, y float64) bool {
			c := s.Box[j]
			return x >= c.X && x < c.X+c.W && y >= c.Y && y < c.Y+c.H
		}
		eligible := func(j int32) bool {
			return s.Paint[j] > s.Paint[i] && !a.isAncestor(j, i) && !a.isAncestor(i, j)
		}
		for _, j := range cov {
			if eligible(j) && hit(j, cx, cy) && (best == snapshot.None || s.Paint[j] > s.Paint[best]) {
				best = j
			}
		}
		if best == snapshot.None {
			continue
		}
		pts := [][2]float64{{b.X + 1, b.Y + 1}, {b.X + b.W - 1, b.Y + 1}, {b.X + 1, b.Y + b.H - 1}, {b.X + b.W - 1, b.Y + b.H - 1}}
		if b.W < 4 || b.H < 4 {
			pts = nil
		}
		all := true
		for _, pt := range pts {
			found := false
			for _, j := range cov {
				if eligible(j) && hit(j, pt[0], pt[1]) {
					found = true
					break
				}
			}
			if !found {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		if m, ok := a.backdrops[best]; ok {
			best = m
		}
		a.nodes[i].CoveredBy = best
	}
}
