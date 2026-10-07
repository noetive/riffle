package facts_test

import (
	"testing"

	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

func dialogFlags(tag string, box snapshot.Rect, opts ...pb.Opt) (modal, covers bool) {
	b := pb.New(1280, 800)
	all := append([]pb.Opt{pb.Attr("role", "dialog")}, opts...)
	i := b.El(b.Body(), tag, box, all...)
	n := analyze(b).Nodes[i]
	return n.Modal, n.Covers
}

func TestDialogModalityByPositionAndSize(t *testing.T) {
	for _, c := range []struct {
		name                 string
		tag                  string
		box                  snapshot.Rect
		opts                 []pb.Opt
		wantModal, wantCover bool
	}{
		{"fixed half viewport", "div", R(0, 0, 1280, 400), []pb.Opt{pb.Position("fixed")}, true, true},
		{"fixed just under half", "div", R(0, 0, 1280, 399), []pb.Opt{pb.Position("fixed")}, false, false},
		{"absolute half viewport", "div", R(0, 0, 640, 800), []pb.Opt{pb.Position("absolute")}, true, true},
		{"static and huge", "div", R(0, 0, 1280, 800), nil, false, false},
		{"relative and huge", "div", R(0, 0, 1280, 800), []pb.Opt{pb.Position("relative")}, false, false},
		{"static aria-modal", "div", R(100, 100, 300, 150), []pb.Opt{pb.Attr("aria-modal", "true")}, true, false},
		{"static aria-modal any case", "div", R(100, 100, 300, 150), []pb.Opt{pb.Attr("aria-modal", " TRUE ")}, true, false},
		{"small fixed", "div", R(100, 100, 300, 150), []pb.Opt{pb.Position("fixed")}, false, false},
		{"small aria-modal", "div", R(100, 100, 300, 150), []pb.Opt{pb.Position("fixed"), pb.Attr("aria-modal", "true")}, true, false},
		{"aria-modal false", "div", R(100, 100, 300, 150), []pb.Opt{pb.Position("fixed"), pb.Attr("aria-modal", "false")}, false, false},
		{"native dialog fixed", "dialog", R(100, 100, 300, 150), []pb.Opt{pb.Position("fixed")}, true, true},
		{"native dialog absolute", "dialog", R(100, 100, 300, 150), []pb.Opt{pb.Position("absolute")}, false, false},
		{"hidden", "div", R(0, 0, 1280, 800), []pb.Opt{pb.Position("fixed"), pb.NotLaid()}, false, false},
		{"offscreen", "div", R(-5000, 0, 1280, 800), []pb.Opt{pb.Position("fixed")}, false, false},
	} {
		modal, covers := dialogFlags(c.tag, c.box, c.opts...)
		if modal != c.wantModal || covers != c.wantCover {
			t.Errorf("%s: modal=%v covers=%v, want %v %v", c.name, modal, covers, c.wantModal, c.wantCover)
		}
	}
}

func TestAlertDialogIsModalAndOtherRolesAreNot(t *testing.T) {
	b := pb.New(1280, 800)
	al := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Attr("role", "alertdialog"), pb.Position("fixed"))
	menu := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Attr("role", "menu"), pb.Position("fixed"))
	p := analyze(b)
	if !p.Nodes[al].Modal {
		t.Error("alertdialog is modal")
	}
	if p.Nodes[menu].Modal {
		t.Error("a menu is not a dialog")
	}
}

func TestBackdropAncestorDepth(t *testing.T) {
	for levels := 1; levels <= 4; levels++ {
		b := pb.New(1280, 800)
		parent := b.Body()
		backdrop := b.El(parent, "div", R(0, 0, 1280, 800), pb.Position("fixed"))
		parent = backdrop
		for k := 1; k < levels; k++ {
			parent = b.El(parent, "div", R(0, 0, 400, 300))
		}
		dlg := b.El(parent, "div", R(100, 100, 300, 150), pb.Attr("role", "dialog"), pb.Position("fixed"))
		n := analyze(b).Nodes[dlg]
		want := levels <= 3
		if n.Modal != want || n.Covers != want {
			t.Errorf("backdrop %d levels up: modal=%v covers=%v, want %v", levels, n.Modal, n.Covers, want)
		}
	}
}

func TestBackdropMustBeLargePositionedAndVisible(t *testing.T) {
	for _, c := range []struct {
		name string
		box  snapshot.Rect
		opts []pb.Opt
		want bool
	}{
		{"large fixed", R(0, 0, 1280, 800), []pb.Opt{pb.Position("fixed")}, true},
		{"large absolute", R(0, 0, 1280, 800), []pb.Opt{pb.Position("absolute")}, true},
		{"large static", R(0, 0, 1280, 800), nil, false},
		{"small fixed", R(0, 0, 200, 200), []pb.Opt{pb.Position("fixed")}, false},
		{"hidden large fixed", R(0, 0, 1280, 800), []pb.Opt{pb.Position("fixed"), pb.NotLaid()}, false},
	} {
		b := pb.New(1280, 800)
		b.El(b.Body(), "div", c.box, c.opts...)
		dlg := b.El(b.Body(), "div", R(100, 100, 300, 150), pb.Attr("role", "dialog"), pb.Position("fixed"))
		p := analyze(b)
		if p.Nodes[dlg].Modal != c.want || p.Nodes[dlg].Covers != c.want {
			t.Errorf("sibling %s: modal=%v covers=%v, want %v", c.name, p.Nodes[dlg].Modal, p.Nodes[dlg].Covers, c.want)
		}
	}
}

func TestBackdropCoveragePointsAtTheDialog(t *testing.T) {
	b := pb.New(1280, 800)
	page := b.El(b.Body(), "button", R(20, 20, 100, 30))
	back := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Fill("rgba(0, 0, 0, 0.5)"))
	dlg := b.El(b.Body(), "div", R(400, 250, 480, 300), pb.Attr("role", "dialog"), pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)"))
	inDlg := b.El(dlg, "button", R(420, 480, 100, 30))
	p := analyze(b)
	if got := p.Nodes[page].CoveredBy; got != dlg {
		t.Errorf("page button covered by %d, want dialog %d (backdrop %d)", got, dlg, back)
	}
	if p.Nodes[inDlg].CoveredBy != snapshot.None {
		t.Error("dialog content is not covered by its own backdrop")
	}
}

func coveredBy(target snapshot.Rect, cover snapshot.Rect, coverOpts ...pb.Opt) (int32, int32) {
	b := pb.New(1280, 800)
	t := b.El(b.Body(), "button", target)
	opts := append([]pb.Opt{pb.Fill("rgb(0, 0, 0)")}, coverOpts...)
	c := b.El(b.Body(), "div", cover, opts...)
	return analyze(b).Nodes[t].CoveredBy, c
}

func TestCoverageNeedsEverySamplePoint(t *testing.T) {
	target := R(100, 100, 100, 30)
	for _, c := range []struct {
		name  string
		cover snapshot.Rect
		want  bool
	}{
		{"exact", R(100, 100, 100, 30), true},
		{"larger", R(50, 50, 300, 200), true},
		{"left corner margin", R(101, 100, 100, 30), true},
		{"left corner missed", R(102, 100, 100, 30), false},
		{"top corner margin", R(100, 101, 100, 30), true},
		{"top corner missed", R(100, 102, 100, 30), false},
		{"right edge reaches corner", R(100, 100, 100, 30), true},
		{"right edge short", R(100, 100, 99, 30), false},
		{"bottom edge reaches corner", R(100, 100, 100, 30), true},
		{"bottom edge short", R(100, 100, 100, 29), false},
		{"disjoint", R(500, 500, 100, 30), false},
	} {
		got, id := coveredBy(target, c.cover)
		if (got == id) != c.want || (!c.want && got != snapshot.None) {
			t.Errorf("%s: CoveredBy = %d (cover %d), want covered=%v", c.name, got, id, c.want)
		}
	}
}

func TestSmallTargetsAreJudgedByTheirCenter(t *testing.T) {
	// Only a sliver around the centre is covered: enough for a box thinner
	// than the corner inset, not for a normal one.
	thin, id := coveredBy(R(100, 100, 3, 30), R(101.4, 90, 0.5, 60))
	if thin != id {
		t.Error("a 3px wide box is covered when its centre is")
	}
	short, id := coveredBy(R(100, 100, 100, 3), R(90, 101.4, 200, 0.5))
	if short != id {
		t.Error("a 3px high box is covered when its centre is")
	}
	four, _ := coveredBy(R(100, 100, 4, 30), R(101.5, 90, 1, 60))
	if four != snapshot.None {
		t.Error("a 4px wide box is sampled at its corners")
	}
	four, _ = coveredBy(R(100, 100, 100, 4), R(90, 101.5, 200, 1))
	if four != snapshot.None {
		t.Error("a 4px high box is sampled at its corners")
	}
}

func TestCoverersAreOpaqueEnoughOrReplaced(t *testing.T) {
	target := R(100, 100, 100, 30)
	for _, c := range []struct {
		name string
		tag  string
		opts []pb.Opt
		want bool
	}{
		{"alpha 0.05", "div", []pb.Opt{pb.Fill("rgba(0, 0, 0, 0.05)")}, true},
		{"alpha 0.04", "div", []pb.Opt{pb.Fill("rgba(0, 0, 0, 0.04)")}, false},
		{"no fill", "div", nil, false},
		{"image", "img", nil, true},
		{"video", "video", nil, true},
		{"canvas", "canvas", nil, true},
		{"iframe", "iframe", nil, true},
		{"svg", "svg", nil, true},
		{"embed", "embed", nil, true},
		{"object", "object", nil, true},
		{"fill but hidden", "div", []pb.Opt{pb.Fill("rgb(0, 0, 0)"), pb.NotLaid()}, false},
		{"fill but opacity 0", "div", []pb.Opt{pb.Fill("rgb(0, 0, 0)"), pb.Style(snapshot.Opacity, "0")}, false},
		{"fill but pointer-events none", "div", []pb.Opt{pb.Fill("rgb(0, 0, 0)"), pb.Style(snapshot.PointerEvents, "none")}, false},
		{"fill but zero size", "div", []pb.Opt{pb.Fill("rgb(0, 0, 0)"), pb.Box(R(100, 100, 0, 0))}, false},
	} {
		b := pb.New(1280, 800)
		tg := b.El(b.Body(), "button", target)
		opts := append([]pb.Opt{}, c.opts...)
		cv := b.El(b.Body(), c.tag, R(50, 50, 300, 200), opts...)
		got := analyze(b).Nodes[tg].CoveredBy
		if (got == cv) != c.want {
			t.Errorf("%s: CoveredBy = %d, want covered=%v", c.name, got, c.want)
		}
	}
}

func TestCoverageFollowsPaintOrder(t *testing.T) {
	b := pb.New(1280, 800)
	under := b.El(b.Body(), "div", R(50, 50, 300, 200), pb.Fill("rgb(0, 0, 0)"), pb.Paint(1))
	tg := b.El(b.Body(), "button", R(100, 100, 100, 30), pb.Paint(5))
	over := b.El(b.Body(), "div", R(50, 50, 300, 200), pb.Fill("rgb(0, 0, 0)"), pb.Paint(9))
	p := analyze(b)
	if got := p.Nodes[tg].CoveredBy; got != over {
		t.Errorf("CoveredBy = %d, want the later painted %d (not %d)", got, over, under)
	}
	if p.Nodes[over].CoveredBy != snapshot.None {
		t.Error("the topmost box is never covered")
	}
	if p.Nodes[under].CoveredBy != over {
		t.Error("the lowest box is covered by the one over it")
	}
}

func TestCoverageCanBeSplitAcrossBoxesAndPicksTopmostAtCenter(t *testing.T) {
	b := pb.New(1280, 800)
	tg := b.El(b.Body(), "button", R(100, 100, 100, 30))
	left := b.El(b.Body(), "div", R(90, 90, 60, 60), pb.Fill("rgb(0, 0, 0)"))
	right := b.El(b.Body(), "div", R(150, 90, 60, 60), pb.Fill("rgb(0, 0, 0)"))
	p := analyze(b)
	if got := p.Nodes[tg].CoveredBy; got != right {
		t.Errorf("CoveredBy = %d, want %d (left is %d)", got, right, left)
	}
}

func TestCoverageSkipsPageRootsAndNonElements(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(100, 100, 100, 30))
	tx := b.Text(d, "words")
	b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Fill("rgb(0, 0, 0)"))
	p := analyze(b)
	if p.Nodes[b.Body()].CoveredBy != snapshot.None || p.Nodes[0].CoveredBy != snapshot.None {
		t.Error("body and document are never covered")
	}
	if p.Nodes[tx].CoveredBy != snapshot.None {
		t.Error("text nodes carry no coverage")
	}
	if p.Nodes[d].CoveredBy == snapshot.None {
		t.Error("the div is covered")
	}
}

func TestHiddenAndEmptyTargetsAreNeverCovered(t *testing.T) {
	b := pb.New(1280, 800)
	hid := b.El(b.Body(), "button", R(100, 100, 100, 30), pb.NotLaid())
	empty := b.El(b.Body(), "button", R(100, 100, 0, 0))
	off := b.El(b.Body(), "button", R(-500, 100, 100, 30))
	b.El(b.Body(), "div", R(-600, 0, 2000, 800), pb.Fill("rgb(0, 0, 0)"))
	p := analyze(b)
	for name, i := range map[string]int32{"hidden": hid, "empty": empty, "offscreen": off} {
		if p.Nodes[i].CoveredBy != snapshot.None {
			t.Errorf("%s target covered", name)
		}
	}
}

func TestEverythingOutsideANativeModalDialogIsCoveredByIt(t *testing.T) {
	b := pb.New(1280, 800)
	behind := b.El(b.Body(), "button", R(0, 0, 100, 30))
	dlg := b.El(b.Body(), "dialog", R(400, 300, 300, 150), pb.Position("fixed"), pb.Attr("open", ""))
	inside := b.El(dlg, "button", R(410, 400, 80, 30))
	plain := b.El(b.Body(), "div", R(0, 100, 100, 30), pb.Position("absolute"))
	plainDlg := b.El(plain, "dialog", R(0, 100, 100, 30), pb.Position("absolute"), pb.Attr("open", ""))
	p := analyze(b)
	if got := p.Nodes[behind].CoveredBy; got != dlg {
		t.Errorf("a control behind a showModal() dialog is covered by it, got %d want %d", got, dlg)
	}
	if got := p.Nodes[inside].CoveredBy; got != snapshot.None {
		t.Errorf("a control inside the dialog is not covered, got %d", got)
	}
	if got := p.Nodes[dlg].CoveredBy; got != snapshot.None {
		t.Errorf("the dialog is not covered by itself, got %d", got)
	}
	if got := p.Nodes[plain].CoveredBy; got != dlg {
		t.Errorf("other page content is covered too, got %d", got)
	}
	_ = plainDlg
}

func TestNonModalDialogLeavesThePageUsable(t *testing.T) {
	b := pb.New(1280, 800)
	behind := b.El(b.Body(), "button", R(0, 0, 100, 30))
	b.El(b.Body(), "dialog", R(400, 300, 300, 150), pb.Position("absolute"), pb.Attr("open", ""))
	if got := analyze(b).Nodes[behind].CoveredBy; got != snapshot.None {
		t.Errorf("show() does not make the page inert, but button is covered by %d", got)
	}
}

// WAI-ARIA makes aria-modal independent of how the dialog box is placed: a
// static dialog in a fixed full-page container is the layer in front, and
// the container is its backdrop.
func TestAnAriaModalDialogInAFixedContainerIsTheLayerInFront(t *testing.T) {
	b := pb.New(1280, 800)
	page := b.El(b.Body(), "button", R(20, 20, 100, 30))
	b.Text(page, "Page action")
	box := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Style(snapshot.BackgroundColor, "rgba(0, 0, 0, 0.5)"))
	dlg := b.El(box, "div", R(440, 100, 400, 200), pb.Attr("role", "dialog"), pb.Attr("aria-modal", "true"), pb.Style(snapshot.BackgroundColor, "rgb(255, 255, 255)"))
	p := analyze(b)
	if !p.Nodes[dlg].Modal || !p.Nodes[dlg].Covers {
		t.Fatalf("modal=%v covers=%v, want a modal that covers the page", p.Nodes[dlg].Modal, p.Nodes[dlg].Covers)
	}
	if got := p.Nodes[page].CoveredBy; got != dlg {
		t.Errorf("page control covered by %d, want the dialog %d", got, dlg)
	}
}

// A dialog in the page's flow is not given a backdrop by a large layer beside
// it that belongs to the page, such as a section's background.
func TestAnInFlowAriaModalDialogTakesNoBackdropFromThePage(t *testing.T) {
	b := pb.New(1280, 800)
	section := b.El(b.Body(), "section", R(0, 0, 1280, 800), pb.Position("relative"))
	b.El(section, "div", R(0, 0, 1280, 800), pb.Position("absolute"), pb.Fill("rgb(0, 0, 80)"))
	dlg := b.El(section, "div", R(440, 100, 400, 200), pb.Attr("role", "dialog"), pb.Attr("aria-modal", "true"))
	n := analyze(b).Nodes[dlg]
	if !n.Modal || n.Covers {
		t.Errorf("modal=%v covers=%v, want a modal that does not cover the page", n.Modal, n.Covers)
	}
}

// A large dialog in the page's flow pushes the page aside; it covers nothing.
func TestALargeInFlowAriaModalDialogDoesNotCoverThePage(t *testing.T) {
	b := pb.New(1280, 800)
	dlg := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Attr("role", "dialog"), pb.Attr("aria-modal", "true"))
	n := analyze(b).Nodes[dlg]
	if !n.Modal || n.Covers {
		t.Errorf("modal=%v covers=%v, want modal without covering", n.Modal, n.Covers)
	}
}

// A box listed only for the fixed panel it holds covers nothing with its own
// box: the page under it stays reachable.
func TestABoxShownForWhatItHoldsCoversNothingItself(t *testing.T) {
	b := pb.New(1280, 800)
	buy := b.El(b.Body(), "button", R(100, 100, 100, 40), pb.Paint(1))
	b.Text(buy, "Buy")
	root := b.El(b.Body(), "div", R(0, 790, 1280, 0), pb.Style(snapshot.OverflowY, "hidden"), pb.Style(snapshot.OverflowX, "hidden"))
	sheet := b.El(root, "div", R(0, 0, 1280, 780), pb.Fill("rgb(255, 255, 255)"), pb.Paint(5))
	b.El(sheet, "div", R(900, 600, 300, 100), pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)"), pb.Paint(6))
	p := analyze(b)
	if !p.Nodes[sheet].Visible() || !p.Nodes[sheet].Holds {
		t.Fatalf("sheet visible=%v holds=%v, want it listed for what it holds", p.Nodes[sheet].Visible(), p.Nodes[sheet].Holds)
	}
	if got := p.Nodes[buy].CoveredBy; got != snapshot.None {
		t.Errorf("Buy covered by %d; a box clipped away covers nothing", got)
	}
}
