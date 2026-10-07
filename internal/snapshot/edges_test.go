package snapshot_test

import (
	"testing"

	"github.com/noetive/riffle/internal/snapshot"
)

// strings: 0 url, 1 title, 2 #document, 3 DIV, 4 a, 5 1, 6 b, 7 2, 8 c, 9 3,
// 10 before, 11 after, 12 marker, 13 v, 14 display, 15 block
// Nodes: 0 document, 1 DIV (attrs a=1 b=2 c=3), 2 DIV with a parent index equal
// to the node count, 3 DIV with a parent far outside, 4 DIV child of 1.
const edgeCapture = `{"documents":[{
 "documentURL":0,"title":1,"scrollOffsetX":7,"scrollOffsetY":11,"contentWidth":1280,"contentHeight":2000,
 "nodes":{
  "parentIndex":[-1,0,5,40,1],
  "nodeType":[9,1,1,1,1],
  "nodeName":[2,3,3,3,16],
  "backendNodeId":[100,101,102,103,104],
  "attributes":[[],[4,5,6,7,8,9],[],[],[]],
  "isClickable":{"index":[5,4]},
  "pseudoType":{"index":[1,2,3,5,4],"value":[10,11,12,10]},
  "inputValue":{"index":[1,5],"value":[13,13]},
  "textValue":{"index":[4,5],"value":[13,13]}
 },
 "layout":{
  "nodeIndex":[0,-1,5,2,1],
  "styles":[[15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15,15],[14],[14],[15],[15]],
  "bounds":[[0,0,1,1],[0,0,1,1],[0,0,1,1],[0,0,1,1],[0,0,1,1]],
  "scrollRects":[[0,0,0,0],[0,0,0,0],[0,0,0,0],[3,4,5,6],[0,0,0]]
 }}],
 "strings":["http://e.test/","E","#document","DIV","a","1","b","2","c","3","before","after","marker","v","display","block"]}`

func parseEdge(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	s, err := snapshot.Parse([]byte(edgeCapture))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseKeepsViewportScrollAndContentExtent(t *testing.T) {
	s := parseEdge(t)
	if s.ScrollX != 7 || s.ScrollY != 11 {
		t.Errorf("scroll = %v,%v", s.ScrollX, s.ScrollY)
	}
	if s.ContentW != 1280 || s.ContentH != 2000 {
		t.Errorf("content = %vx%v", s.ContentW, s.ContentH)
	}
	if s.ViewportW != 1280 || s.ViewportH != 2000 {
		t.Errorf("viewport falls back to the content extent: %vx%v", s.ViewportW, s.ViewportH)
	}
}

func TestParseTreeShapeAndNodeKinds(t *testing.T) {
	s := parseEdge(t)
	if s.Kind[0] != snapshot.KindDocument || s.Kind[1] != snapshot.KindElement {
		t.Errorf("kinds: %v", s.Kind)
	}
	if s.Parent[0] != snapshot.None || s.Parent[1] != 0 || s.Parent[4] != 1 {
		t.Errorf("parents: %v", s.Parent)
	}
	if got := s.Children[0]; len(got) != 1 || got[0] != 1 {
		t.Errorf("the node at index 0 can be a parent: %v", got)
	}
	if s.Parent[2] != snapshot.None || s.Parent[3] != snapshot.None {
		t.Errorf("a parent outside the document means no parent: %v", s.Parent)
	}
	if s.ByBackend(100) != 0 || s.ByBackend(100) == snapshot.None {
		t.Error("index 0 is a valid node, distinct from None")
	}
	if snapshot.None >= 0 {
		t.Error("None must not collide with a node index")
	}
}

func TestParseStringIndexPastTheTableIsEmpty(t *testing.T) {
	s := parseEdge(t)
	if s.Tag[4] != "" {
		t.Errorf("string index past the table must read as empty, got %q", s.Tag[4])
	}
	if s.Tag[1] != "div" {
		t.Errorf("tag = %q", s.Tag[1])
	}
}

func TestParseKeepsEveryAttributeInOrder(t *testing.T) {
	s := parseEdge(t)
	want := []snapshot.Attr{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}, {Name: "c", Value: "3"}}
	if len(s.Attrs[1]) != 3 {
		t.Fatalf("attrs = %+v", s.Attrs[1])
	}
	for i, a := range want {
		if s.Attrs[1][i] != a {
			t.Errorf("attr %d = %+v want %+v", i, s.Attrs[1][i], a)
		}
	}
	if v, ok := s.Attr(1, "c"); !ok || v != "3" {
		t.Errorf("Attr(c) = %q %v", v, ok)
	}
	if v, ok := s.Attr(1, "b"); !ok || v != "2" {
		t.Errorf("Attr(b) = %q %v", v, ok)
	}
	if v, ok := s.Attr(1, "zzz"); ok || v != "" {
		t.Errorf("an absent attribute must not be reported: %q %v", v, ok)
	}
}

func TestParseRareDataAtTheNodeCountIsIgnored(t *testing.T) {
	s := parseEdge(t) // node count is 5; entries at index 5 must not appear or panic
	if s.Len() != 5 {
		t.Fatalf("len = %d", s.Len())
	}
	if !s.Clickable[4] {
		t.Error("clickable[4] lost")
	}
	if s.Value[1] != "v" || s.Value[4] != "v" {
		t.Errorf("values = %q", s.Value)
	}
}

func TestParseKnowsAllGeneratedBoxKinds(t *testing.T) {
	s := parseEdge(t)
	if s.Pseudo[1] != "before" || s.Pseudo[2] != "after" || s.Pseudo[3] != "marker" {
		t.Errorf("pseudo = %q", s.Pseudo)
	}
}

func TestParseSkipsInvalidLayoutEntriesButKeepsLaterOnes(t *testing.T) {
	s := parseEdge(t)
	if !s.Laid[0] {
		t.Error("node 0 has a layout entry")
	}
	if !s.Laid[2] || !s.Laid[1] {
		t.Errorf("entries after an invalid one must still apply: laid=%v", s.Laid)
	}
	if s.Laid[3] || s.Laid[4] {
		t.Errorf("nodes without a layout entry are not laid: %v", s.Laid)
	}
}

func TestParseReadsScrollExtentOrigin(t *testing.T) {
	s := parseEdge(t)
	if s.Scroll[2] != (snapshot.Rect{X: 3, Y: 4, W: 5, H: 6}) {
		t.Errorf("scroll = %+v", s.Scroll[2])
	}
}

func TestParseIgnoresStylesBeyondTheKnownProperties(t *testing.T) {
	s := parseEdge(t)
	if s.Prop(0, snapshot.Order) != "block" || s.Prop(0, snapshot.Display) != "block" {
		t.Errorf("known properties lost: %q", s.Prop(0, snapshot.Order))
	}
}

func TestPropNamesAreTheEngineStyleNames(t *testing.T) {
	want := map[snapshot.Prop]string{
		snapshot.Display: "display", snapshot.Visibility: "visibility", snapshot.Opacity: "opacity",
		snapshot.Position: "position", snapshot.ZIndex: "z-index", snapshot.OverflowX: "overflow-x",
		snapshot.OverflowY: "overflow-y", snapshot.Color: "color", snapshot.BackgroundColor: "background-color",
		snapshot.FontSize: "font-size", snapshot.FontWeight: "font-weight", snapshot.FontStyle: "font-style",
		snapshot.TextDecorationLine: "text-decoration-line", snapshot.TextOverflow: "text-overflow",
		snapshot.WhiteSpace: "white-space", snapshot.LineClamp: "-webkit-line-clamp", snapshot.Cursor: "cursor",
		snapshot.PointerEvents: "pointer-events", snapshot.ClipPath: "clip-path", snapshot.Float: "float",
		snapshot.Transform: "transform", snapshot.TextIndent: "text-indent",
		snapshot.FlexDirection: "flex-direction", snapshot.Order: "order",
		snapshot.BackgroundImage: "background-image", snapshot.BackgroundClip: "background-clip",
	}
	if len(want) != int(snapshot.NumProps) {
		t.Fatalf("every property needs a name: %d of %d", len(want), snapshot.NumProps)
	}
	for p, name := range want {
		if snapshot.PropNames[p] != name {
			t.Errorf("PropNames[%d] = %q, want %q", p, snapshot.PropNames[p], name)
		}
	}
}

func TestPropIsEmptyForUnlaidNodes(t *testing.T) {
	s := &snapshot.Snapshot{
		Laid:  []bool{false, true},
		Style: make([][snapshot.NumProps]string, 2),
	}
	s.Style[0][snapshot.Display] = "block"
	s.Style[1][snapshot.Display] = "flex"
	if s.Prop(0, snapshot.Display) != "" {
		t.Error("an unlaid node has no computed style")
	}
	if s.Prop(1, snapshot.Display) != "flex" {
		t.Error("a laid node reports its style")
	}
}

func TestAncestorsWalkNearestFirstAndStopOnRequest(t *testing.T) {
	s := &snapshot.Snapshot{Parent: []int32{snapshot.None, 0, 1, 2}}
	var got []int32
	s.Ancestors(3, func(i int32) bool { got = append(got, i); return true })
	if len(got) != 3 || got[0] != 2 || got[1] != 1 || got[2] != 0 {
		t.Errorf("ancestors = %v", got)
	}
	got = nil
	s.Ancestors(3, func(i int32) bool { got = append(got, i); return i != 1 })
	if len(got) != 2 || got[1] != 1 {
		t.Errorf("walk must stop when asked: %v", got)
	}
	got = nil
	s.Ancestors(0, func(i int32) bool { got = append(got, i); return true })
	if len(got) != 0 {
		t.Errorf("the root has no ancestors: %v", got)
	}
}

func TestParentsMustPrecedeTheirChildrenSoTheTreeCannotLoop(t *testing.T) {
	// Nodes 0 and 1 name each other as parent; node 3 names a later node.
	s, err := snapshot.Parse([]byte(`{"documents":[{"nodes":{"parentIndex":[1,0,0,4,0],"nodeType":[9,1,1,3,1],"nodeName":[0,1,1,2,1],"nodeValue":[-1,-1,-1,3,-1],"backendNodeId":[1,2,3,4,5]}}],"strings":["#document","button","","hi"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range s.Parent {
		if p != snapshot.None && int(p) >= i {
			t.Errorf("node %d has parent %d, which does not come before it", i, p)
		}
	}
	for i := range s.Parent {
		for _, c := range s.Children[i] {
			if int(c) <= i {
				t.Errorf("node %d lists child %d, which does not come after it", i, c)
			}
		}
	}
}
