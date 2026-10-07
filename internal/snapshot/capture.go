package snapshot

import (
	"fmt"

	"github.com/tidwall/gjson"
)

// Parse builds a Snapshot from the result of DOMSnapshot.captureSnapshot,
// requested with PropNames as computedStyles and with DOM rects, paint order
// and blended background colors included. Only the first (top) document is read;
// frame documents are reached through their own captures.
func Parse(result []byte) (*Snapshot, error) {
	r := gjson.ParseBytes(result)
	docs := r.Get("documents")
	if !docs.Exists() || len(docs.Array()) == 0 {
		return nil, fmt.Errorf("snapshot: capture has no documents")
	}
	strs := r.Get("strings").Array()
	str := func(idx int64) string {
		if idx < 0 || int(idx) >= len(strs) {
			return ""
		}
		return strs[idx].String()
	}
	doc := docs.Array()[0]

	nodes := doc.Get("nodes")
	parents := nodes.Get("parentIndex").Array()
	n := len(parents)
	s := &Snapshot{
		URL:        str(doc.Get("documentURL").Int()),
		Title:      str(doc.Get("title").Int()),
		ViewportW:  doc.Get("contentWidth").Float(), // refined by caller via layout metrics
		ViewportH:  doc.Get("contentHeight").Float(),
		ScrollX:    doc.Get("scrollOffsetX").Float(),
		ScrollY:    doc.Get("scrollOffsetY").Float(),
		ContentW:   doc.Get("contentWidth").Float(),
		ContentH:   doc.Get("contentHeight").Float(),
		Parent:     make([]int32, n),
		Kind:       make([]NodeKind, n),
		Tag:        make([]string, n),
		Text:       make([]string, n),
		Attrs:      make([][]Attr, n),
		Backend:    make([]int64, n),
		Clickable:  make([]bool, n),
		Ticked:     make([]bool, n),
		Pseudo:     make([]string, n),
		Value:      make([]string, n),
		Laid:       make([]bool, n),
		Box:        make([]Rect, n),
		Style:      make([][NumProps]string, n),
		Paint:      make([]int32, n),
		Scroll:     make([]Rect, n),
		Background: make([]string, n),
		Children:   make([][]int32, n),
	}

	names := nodes.Get("nodeName").Array()
	types := nodes.Get("nodeType").Array()
	values := nodes.Get("nodeValue").Array()
	backend := nodes.Get("backendNodeId").Array()
	attrs := nodes.Get("attributes").Array()
	// The arrays describe the same nodes, but a short one must read as absent
	// values, not as an index past its end.
	at := func(a []gjson.Result, i int) gjson.Result {
		if i < len(a) {
			return a[i]
		}
		return gjson.Result{}
	}
	for i := 0; i < n; i++ {
		p := int32(parents[i].Int())
		s.Parent[i] = p
		// A parent comes before its children in document order; anything else
		// would let the tree loop.
		if p >= 0 && int(p) < i {
			s.Children[p] = append(s.Children[p], int32(i))
		} else {
			s.Parent[i] = None
		}
		switch at(types, i).Int() {
		case 1:
			s.Kind[i] = KindElement
			s.Tag[i] = lower(str(at(names, i).Int()))
		case 3:
			s.Kind[i] = KindText
			s.Text[i] = str(at(values, i).Int())
		case 9:
			s.Kind[i] = KindDocument
		}
		s.Backend[i] = at(backend, i).Int()
		if i < len(attrs) {
			a := attrs[i].Array()
			for j := 0; j+1 < len(a); j += 2 {
				s.Attrs[i] = append(s.Attrs[i], Attr{Name: str(a[j].Int()), Value: str(a[j+1].Int())})
			}
		}
	}
	for _, v := range nodes.Get("isClickable.index").Array() {
		if i := int(v.Int()); i >= 0 && i < n {
			s.Clickable[i] = true
		}
	}
	for _, v := range nodes.Get("inputChecked.index").Array() {
		if i := int(v.Int()); i >= 0 && i < n {
			s.Ticked[i] = true
		}
	}
	for _, v := range nodes.Get("optionSelected.index").Array() {
		if i := int(v.Int()); i >= 0 && i < n {
			s.Ticked[i] = true
		}
	}
	forRLE(nodes.Get("pseudoType"), func(i int, v gjson.Result) {
		if i >= 0 && i < n {
			s.Pseudo[i] = pseudoName(str(v.Int()))
		}
	})
	forRLE(nodes.Get("inputValue"), func(i int, v gjson.Result) {
		if i >= 0 && i < n {
			s.Value[i] = str(v.Int())
		}
	})
	forRLE(nodes.Get("textValue"), func(i int, v gjson.Result) {
		if i >= 0 && i < n {
			s.Value[i] = str(v.Int())
		}
	})

	layout := doc.Get("layout")
	idx := layout.Get("nodeIndex").Array()
	styles := layout.Get("styles").Array()
	bounds := layout.Get("bounds").Array()
	paints := layout.Get("paintOrders").Array()
	texts := layout.Get("text").Array()
	scrolls := layout.Get("scrollRects").Array()
	bgs := layout.Get("blendedBackgroundColors").Array()
	for k := range idx {
		i := int(idx[k].Int())
		if i < 0 || i >= n {
			continue
		}
		s.Laid[i] = true
		if k < len(bounds) {
			b := bounds[k].Array()
			if len(b) == 4 {
				s.Box[i] = Rect{b[0].Float(), b[1].Float(), b[2].Float(), b[3].Float()}
			}
		}
		if k < len(styles) {
			for p, v := range styles[k].Array() {
				if p < int(NumProps) {
					s.Style[i][p] = str(v.Int())
				}
			}
		}
		if k < len(paints) {
			s.Paint[i] = int32(paints[k].Int())
		}
		// The DOM text keeps the whitespace between lines; the layout text is
		// only used where the DOM has none, as for generated content.
		if k < len(texts) && s.Text[i] == "" {
			s.Text[i] = str(texts[k].Int())
		}
		if k < len(scrolls) {
			b := scrolls[k].Array()
			if len(b) == 4 {
				s.Scroll[i] = Rect{b[0].Float(), b[1].Float(), b[2].Float(), b[3].Float()}
			}
		}
		if k < len(bgs) {
			s.Background[i] = str(bgs[k].Int())
		}
	}
	return s, nil
}

// forRLE walks a CDP RareStringData-style object {index:[], value:[]}.
func forRLE(r gjson.Result, fn func(i int, v gjson.Result)) {
	ix := r.Get("index").Array()
	vs := r.Get("value").Array()
	for k := range ix {
		if k < len(vs) {
			fn(int(ix[k].Int()), vs[k])
		}
	}
}

func pseudoName(t string) string {
	switch t {
	case "before", "after", "marker":
		return t
	}
	return ""
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
