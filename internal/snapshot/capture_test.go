package snapshot_test

import (
	"testing"

	"github.com/noetive/riffle/internal/snapshot"
)

// A two-element page: <body><button>Go</button>text</body>, strings are
// referenced by index as the engine reports them.
const capture = `{"documents":[{
 "documentURL":0,"title":1,"scrollOffsetX":0,"scrollOffsetY":30,"contentWidth":1280,"contentHeight":2000,
 "nodes":{
  "parentIndex":[-1,0,1,2,2],
  "nodeType":[9,1,1,1,3],
  "nodeName":[2,3,4,5,2],
  "nodeValue":[-1,-1,-1,-1,6],
  "backendNodeId":[10,11,12,13,14],
  "attributes":[[],[],[],[7,8],[]],
  "isClickable":{"index":[3]}
 },
 "layout":{
  "nodeIndex":[3,4],
  "styles":[[9,10],[9,10]],
  "bounds":[[10,20,100,40],[10,70,50,10]],
  "paintOrders":[2,1],
  "text":[-1,6]
 }}],
 "strings":["http://x.test/","T","#document","HTML","BODY","BUTTON","  spaced\nout  ","id","go","block","visible"]}`

func TestParseKeepsIdentityStructureAndLayout(t *testing.T) {
	s, err := snapshot.Parse([]byte(capture))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 5 || s.URL != "http://x.test/" || s.Title != "T" || s.ScrollY != 30 {
		t.Fatalf("document facts wrong: len=%d url=%q title=%q scrollY=%v", s.Len(), s.URL, s.Title, s.ScrollY)
	}
	if s.Tag[3] != "button" || s.Backend[3] != 13 || !s.Clickable[3] {
		t.Errorf("button not recognised: tag=%q backend=%d clickable=%v", s.Tag[3], s.Backend[3], s.Clickable[3])
	}
	if v, ok := s.Attr(3, "id"); !ok || v != "go" {
		t.Errorf("attribute lost: %q %v", v, ok)
	}
	if got := s.Children[2]; len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Errorf("children out of order: %v", got)
	}
	if !s.Laid[3] || s.Box[3] != (snapshot.Rect{X: 10, Y: 20, W: 100, H: 40}) || s.Prop(3, snapshot.Display) != "block" {
		t.Errorf("layout lost: laid=%v box=%+v", s.Laid[3], s.Box[3])
	}
	if s.Laid[1] || s.Prop(1, snapshot.Display) != "" {
		t.Error("a node without a box must report no style")
	}
	if s.Text[4] != "  spaced\nout  " {
		t.Errorf("DOM text must keep the whitespace between lines, got %q", s.Text[4])
	}
	if s.ByBackend(14) != 4 || s.ByBackend(999) != snapshot.None {
		t.Error("backend lookup wrong")
	}
}

func TestParseRefusesCaptureWithoutDocuments(t *testing.T) {
	if _, err := snapshot.Parse([]byte(`{"documents":[],"strings":[]}`)); err == nil {
		t.Fatal("an empty capture must be an error, not an empty page")
	}
}

// A page exercising generated content, form values, scroll containers and
// the ways a capture can be ragged: layout arrays shorter than nodeIndex,
// node indexes outside the document and rare-data entries pointing past it.
//
//	0 #document  1 HTML  2 BODY  3 INPUT  4 ::before  5 TEXTAREA
//	6 text (no DOM text)  7 text (DOM text)  8 scroll container
const raggedCapture = `{"documents":[{
 "documentURL":0,"title":1,
 "nodes":{
  "parentIndex":[-1,0,1,2,2,2,2,2,2],
  "nodeType":[9,1,1,1,1,1,3,3,1],
  "nodeName":[2,3,4,5,6,7,-1,-1,8],
  "nodeValue":[-1,-1,-1,-1,-1,-1,-1,13,-1],
  "backendNodeId":[20,21,22,23,24,25,26,27,28],
  "attributes":[[],[],[],[18,19],[],[18]],
  "pseudoType":{"index":[4,2,99],"value":[11,12,11]},
  "inputValue":{"index":[3,99],"value":[9,9]},
  "textValue":{"index":[5,99],"value":[10,10]},
  "isClickable":{"index":[3,99]}
 },
 "layout":{
  "nodeIndex":[3,4,5,6,7,8,-1,99],
  "styles":[[17],[17],[17],[17],[17]],
  "bounds":[[1,2,30,40],[1,2,3],[5,6,7,8],[9,10,11,12],[13,14,15,16]],
  "paintOrders":[5,4,3,2,1],
  "text":[-1,-1,-1,14,15],
  "scrollRects":[[0,0,0,0],[0,0,0,0],[0,0,0,0],[0,0,0,0],[0,0,0,0],[0,0,300,900]],
  "blendedBackgroundColors":[-1,-1,-1,-1,-1,16]
 }}],
 "strings":["http://y.test/","Y","#document","HTML","BODY","INPUT","::before","TEXTAREA","AZaz-@[~",
  "typed","area text","before","first-line","dom","layout fallback","layout ignored","rgba(1,2,3,1)","block","type","text"]}`

func parseRagged(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	s, err := snapshot.Parse([]byte(raggedCapture))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseLowercasesTagsAndOnlyLetters(t *testing.T) {
	s := parseRagged(t)
	for i, want := range map[int32]string{1: "html", 2: "body", 3: "input", 5: "textarea", 8: "azaz-@[~"} {
		if s.Tag[i] != want {
			t.Errorf("tag[%d] = %q, want %q", i, s.Tag[i], want)
		}
	}
	if s.Tag[0] != "" || s.Tag[6] != "" {
		t.Error("non-elements must have no tag")
	}
}

func TestParseKeepsPseudoTypeOnlyForGeneratedBoxes(t *testing.T) {
	s := parseRagged(t)
	if s.Pseudo[4] != "before" {
		t.Errorf("pseudo[4] = %q", s.Pseudo[4])
	}
	if s.Pseudo[2] != "" {
		t.Errorf("an unknown pseudo type must not be reported, got %q", s.Pseudo[2])
	}
	for i, p := range s.Pseudo {
		if p != "" && i != 4 {
			t.Errorf("node %d has pseudo %q", i, p)
		}
	}
}

func TestParseIgnoresClickableIndexesPastTheEnd(t *testing.T) {
	s := parseRagged(t)
	for i, c := range s.Clickable {
		if c != (i == 3) {
			t.Errorf("clickable[%d] = %v", i, c)
		}
	}
}

func TestParseReadsInputAndTextareaValuesAndSkipsPastTheEnd(t *testing.T) {
	s := parseRagged(t)
	if s.Value[3] != "typed" {
		t.Errorf("input value = %q", s.Value[3])
	}
	if s.Value[5] != "area text" {
		t.Errorf("textarea value = %q", s.Value[5])
	}
	if s.Len() != 9 || len(s.Value) != 9 {
		t.Errorf("entries past the end must not grow the snapshot: %d", s.Len())
	}
	for i, v := range s.Value {
		if v != "" && i != 3 && i != 5 {
			t.Errorf("node %d got value %q", i, v)
		}
	}
}

func TestParseLayoutTextOnlyFillsEmptyDOMText(t *testing.T) {
	s := parseRagged(t)
	if s.Text[6] != "layout fallback" {
		t.Errorf("generated text = %q", s.Text[6])
	}
	if s.Text[7] != "dom" {
		t.Errorf("DOM text must win over layout text, got %q", s.Text[7])
	}
	if s.Text[3] != "" {
		t.Errorf("a layout entry without text must leave the node empty, got %q", s.Text[3])
	}
}

func TestParseReadsScrollExtentAndBackground(t *testing.T) {
	s := parseRagged(t)
	if s.Scroll[8] != (snapshot.Rect{W: 300, H: 900}) {
		t.Errorf("scroll = %+v", s.Scroll[8])
	}
	if s.Background[8] != "rgba(1,2,3,1)" {
		t.Errorf("background = %q", s.Background[8])
	}
	for _, i := range []int{3, 4, 5, 6, 7} {
		if s.Background[i] != "" || !s.Scroll[i].Empty() {
			t.Errorf("node %d: scroll=%+v bg=%q", i, s.Scroll[i], s.Background[i])
		}
	}
}

func TestParseSurvivesShortLayoutArraysAndBadIndexes(t *testing.T) {
	s := parseRagged(t)
	for _, i := range []int{3, 4, 5, 6, 7, 8} {
		if !s.Laid[i] {
			t.Errorf("node %d has a layout entry and must be laid", i)
		}
	}
	for _, i := range []int{0, 1, 2} {
		if s.Laid[i] {
			t.Errorf("node %d has no layout entry", i)
		}
	}
	if s.Box[3] != (snapshot.Rect{X: 1, Y: 2, W: 30, H: 40}) {
		t.Errorf("box[3] = %+v", s.Box[3])
	}
	if !s.Box[4].Empty() || s.Box[4] != (snapshot.Rect{}) {
		t.Errorf("a malformed bounds entry must leave the box zero, got %+v", s.Box[4])
	}
	if s.Box[7] != (snapshot.Rect{X: 13, Y: 14, W: 15, H: 16}) {
		t.Errorf("box[7] = %+v", s.Box[7])
	}
	// k=5 (node 8) is past bounds, styles and paintOrders.
	if s.Box[8] != (snapshot.Rect{}) || s.Paint[8] != 0 || s.Prop(8, snapshot.Display) != "" {
		t.Errorf("short arrays must leave defaults: box=%+v paint=%d", s.Box[8], s.Paint[8])
	}
	if s.Paint[3] != 5 || s.Paint[7] != 1 || s.Prop(3, snapshot.Display) != "block" {
		t.Errorf("paint/style lost: %d %d %q", s.Paint[3], s.Paint[7], s.Prop(3, snapshot.Display))
	}
}

func TestParseHandlesShortAttributeArrays(t *testing.T) {
	s := parseRagged(t)
	if v, ok := s.Attr(3, "type"); !ok || v != "text" {
		t.Errorf("attr = %q %v", v, ok)
	}
	if len(s.Attrs[5]) != 0 {
		t.Errorf("a dangling attribute name must be dropped: %+v", s.Attrs[5])
	}
	if len(s.Attrs[8]) != 0 {
		t.Errorf("nodes past the attributes array have none: %+v", s.Attrs[8])
	}
}

func TestRectEmpty(t *testing.T) {
	cases := []struct {
		r    snapshot.Rect
		want bool
	}{
		{snapshot.Rect{W: 1, H: 1}, false},
		{snapshot.Rect{W: 0, H: 1}, true},
		{snapshot.Rect{W: 1, H: 0}, true},
		{snapshot.Rect{W: -1, H: 5}, true},
		{snapshot.Rect{W: 5, H: -1}, true},
		{snapshot.Rect{}, true},
	}
	for _, c := range cases {
		if c.r.Empty() != c.want {
			t.Errorf("%+v Empty = %v", c.r, !c.want)
		}
	}
}

func TestRectCenter(t *testing.T) {
	x, y := snapshot.Rect{X: 10, Y: 20, W: 100, H: 40}.Center()
	if x != 60 || y != 40 {
		t.Errorf("center = %v,%v", x, y)
	}
}

func TestRectIntersects(t *testing.T) {
	a := snapshot.Rect{X: 0, Y: 0, W: 10, H: 10}
	cases := []struct {
		name string
		o    snapshot.Rect
		want bool
	}{
		{"overlap", snapshot.Rect{X: 5, Y: 5, W: 10, H: 10}, true},
		{"contained", snapshot.Rect{X: 2, Y: 2, W: 2, H: 2}, true},
		{"touching right edge", snapshot.Rect{X: 10, Y: 0, W: 5, H: 10}, false},
		{"touching left edge", snapshot.Rect{X: -5, Y: 0, W: 5, H: 10}, false},
		{"touching bottom edge", snapshot.Rect{X: 0, Y: 10, W: 10, H: 5}, false},
		{"touching top edge", snapshot.Rect{X: 0, Y: -5, W: 10, H: 5}, false},
		{"touching corner", snapshot.Rect{X: 10, Y: 10, W: 5, H: 5}, false},
		{"apart", snapshot.Rect{X: 50, Y: 50, W: 5, H: 5}, false},
		{"overlap in x only", snapshot.Rect{X: 5, Y: 50, W: 5, H: 5}, false},
		{"overlap in y only", snapshot.Rect{X: 50, Y: 5, W: 5, H: 5}, false},
	}
	for _, c := range cases {
		if a.Intersects(c.o) != c.want || c.o.Intersects(a) != c.want {
			t.Errorf("%s: Intersects = %v, want %v (and symmetric)", c.name, !c.want, c.want)
		}
	}
}
