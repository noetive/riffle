package facts

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func sameColor(a, b rgba) bool {
	return near(a.r, b.r) && near(a.g, b.g) && near(a.b, b.b) && near(a.a, b.a)
}

func TestParseColorSyntaxes(t *testing.T) {
	for _, c := range []struct {
		in   string
		want rgba
	}{
		{"transparent", rgba{0, 0, 0, 0}},
		{"black", rgba{0, 0, 0, 1}},
		{"white", rgba{255, 255, 255, 1}},
		{"  WHITE ", rgba{255, 255, 255, 1}},
		{"#fff", rgba{255, 255, 255, 1}},
		{"#1a2", rgba{0x11, 0xaa, 0x22, 1}},
		{"#f008", rgba{255, 0, 0, 0x88 / 255.0}},
		{"#102030", rgba{0x10, 0x20, 0x30, 1}},
		{"#10203040", rgba{0x10, 0x20, 0x30, 0x40 / 255.0}},
		{"rgb(1, 2, 3)", rgba{1, 2, 3, 1}},
		{"RGB(1,2,3)", rgba{1, 2, 3, 1}},
		{"rgba(1, 2, 3, 0.5)", rgba{1, 2, 3, 0.5}},
		{"rgb(1 2 3 / 0.25)", rgba{1, 2, 3, 0.25}},
		{"rgba(1, 2, 3, 50%)", rgba{1, 2, 3, 0.5}},
		{"rgb(100%, 50%, 0%)", rgba{255, 127.5, 0, 1}},
		{"rgb(10, 20, 30, 0.1, 99)", rgba{10, 20, 30, 0.1}},
	} {
		got, ok := parseColor(c.in)
		if !ok || !sameColor(got, c.want) {
			t.Errorf("parseColor(%q) = %v %v, want %v", c.in, got, ok, c.want)
		}
	}
}

func TestParseColorRejectsMalformedInput(t *testing.T) {
	for _, in := range []string{"", "   ", "red", "#", "#12", "#12345", "#1234567", "#ggg", "#gggggg", "rgb(1, 2)", "rgb(1, 2, x)", "rgb 1, 2, 3", "rgb)1, 2, 3(", "rgb(1, 2, 3, y)"} {
		if got, ok := parseColor(in); ok {
			t.Errorf("parseColor(%q) = %v, want failure", in, got)
		}
	}
}

func TestColorCompositing(t *testing.T) {
	half := rgba{255, 0, 0, 0.5}
	if got := half.over(rgba{0, 0, 255, 1}); !sameColor(got, rgba{127.5, 0, 127.5, 1}) {
		t.Errorf("half red over blue = %v", got)
	}
	if got := (rgba{10, 20, 30, 1}).over(rgba{200, 200, 200, 1}); !sameColor(got, rgba{10, 20, 30, 1}) {
		t.Errorf("opaque over = %v", got)
	}
	if got := (rgba{10, 20, 30, 0}).over(rgba{200, 100, 50, 1}); !sameColor(got, rgba{200, 100, 50, 1}) {
		t.Errorf("transparent over = %v", got)
	}
	// Two translucent layers: alpha accumulates and color weights by coverage.
	if got := (rgba{100, 0, 0, 0.5}).over(rgba{0, 100, 0, 0.5}); !sameColor(got, rgba{100 * 0.5 / 0.75, 100 * 0.25 / 0.75, 0, 0.75}) {
		t.Errorf("translucent over translucent = %v", got)
	}
	if got := (rgba{1, 2, 3, 0}).over(rgba{4, 5, 6, 0}); got != (rgba{}) {
		t.Errorf("nothing over nothing = %v", got)
	}
}

func TestLuminanceAndContrast(t *testing.T) {
	black, wht := rgba{0, 0, 0, 1}, rgba{255, 255, 255, 1}
	if !near(black.luminance(), 0) || !near(wht.luminance(), 1) {
		t.Errorf("luminance black=%v white=%v", black.luminance(), wht.luminance())
	}
	if got := (rgba{128, 128, 128, 1}).luminance(); math.Abs(got-0.2158605) > 1e-5 {
		t.Errorf("grey 128 luminance = %v", got)
	}
	// The dark end is linear: 10/255 is below the knee.
	if got := (rgba{10, 10, 10, 1}).luminance(); !near(got, 10.0/255/12.92) {
		t.Errorf("grey 10 luminance = %v", got)
	}
	// Channel weights: pure green is brightest, pure blue darkest.
	r, g, b := (rgba{255, 0, 0, 1}).luminance(), (rgba{0, 255, 0, 1}).luminance(), (rgba{0, 0, 255, 1}).luminance()
	if !near(r, 0.2126) || !near(g, 0.7152) || !near(b, 0.0722) {
		t.Errorf("channel weights r=%v g=%v b=%v", r, g, b)
	}
	if got := contrast(black, wht); !near(got, 21) {
		t.Errorf("contrast black/white = %v", got)
	}
	if contrast(black, wht) != contrast(wht, black) {
		t.Error("contrast must not depend on argument order")
	}
	if got := contrast(wht, wht); !near(got, 1) {
		t.Errorf("contrast with self = %v", got)
	}
}

func TestColorDistance(t *testing.T) {
	if got := distance(rgba{0, 0, 0, 1}, rgba{3, 4, 0, 1}); !near(got, 5) {
		t.Errorf("distance = %v", got)
	}
	if got := distance(rgba{0, 0, 0, 1}, rgba{0, 0, 12, 1}); !near(got, 12) {
		t.Errorf("blue-only distance = %v", got)
	}
	if got := distance(rgba{0, 7, 0, 1}, rgba{0, 0, 0, 1}); !near(got, 7) {
		t.Errorf("green-only distance = %v", got)
	}
	if got := distance(rgba{1, 2, 3, 1}, rgba{1, 2, 3, 1}); got != 0 {
		t.Errorf("self distance = %v", got)
	}
}

func TestHSL(t *testing.T) {
	for _, c := range []struct {
		name    string
		in      rgba
		h, s, l float64
	}{
		{"red", rgba{255, 0, 0, 1}, 0, 1, 0.5},
		{"yellow", rgba{255, 255, 0, 1}, 60, 1, 0.5},
		{"green", rgba{0, 255, 0, 1}, 120, 1, 0.5},
		{"cyan", rgba{0, 255, 255, 1}, 180, 1, 0.5},
		{"blue", rgba{0, 0, 255, 1}, 240, 1, 0.5},
		{"magenta", rgba{255, 0, 255, 1}, 300, 1, 0.5},
		{"rose wraps below zero", rgba{255, 0, 128, 1}, 360 - 360*(128.0/255)/6, 1, 0.5},
		{"grey", rgba{100, 100, 100, 1}, 0, 0, 100.0 / 255},
		{"light pink", rgba{255, 128, 128, 1}, 0, 1, (1 + 128.0/255) / 2},
		{"dark red", rgba{128, 0, 0, 1}, 0, 1, 128.0 / 255 / 2},
		{"olive green", rgba{50, 100, 50, 1}, 120, 0.333333333, 75.0 / 255},
	} {
		h, s, l := c.in.hsl()
		if math.Abs(h-c.h) > 1e-3 || math.Abs(s-c.s) > 1e-3 || math.Abs(l-c.l) > 1e-3 {
			t.Errorf("%s: hsl = %v %v %v, want %v %v %v", c.name, h, s, l, c.h, c.s, c.l)
		}
	}
}

func TestParsePx(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
		ok   bool
	}{
		{"16px", 16, true},
		{" 12.5px ", 12.5, true},
		{"-500px", -500, true},
		{"7", 7, true},
		{"0px", 0, true},
		{"", 0, false},
		{"px", 0, false},
		{"  ", 0, false},
		{"big", 0, false},
		{"1em", 0, false},
	} {
		got, ok := parsePx(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parsePx(%q) = %v %v, want %v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
