package facts

import (
	"math"
	"strconv"
	"strings"
)

// rgba is a color with channels in 0..255 and alpha in 0..1.
type rgba struct{ r, g, b, a float64 }

var white = rgba{255, 255, 255, 1}

// parseColor reads the color syntaxes a computed style can carry: rgb(),
// rgba() in comma or space form, #rgb, #rrggbb, #rrggbbaa and transparent.
func parseColor(s string) (rgba, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return rgba{}, false
	case s == "transparent":
		return rgba{}, true
	case s == "black":
		return rgba{0, 0, 0, 1}, true
	case s == "white":
		return white, true
	case s[0] == '#':
		return parseHex(s[1:])
	case strings.HasPrefix(s, "rgb"):
		open := strings.IndexByte(s, '(')
		end := strings.LastIndexByte(s, ')')
		if open < 0 || end < open {
			return rgba{}, false
		}
		f := strings.FieldsFunc(s[open+1:end], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(f) < 3 {
			return rgba{}, false
		}
		var v [4]float64
		v[3] = 1
		for i := 0; i < len(f) && i < 4; i++ {
			t := f[i]
			pct := strings.HasSuffix(t, "%")
			x, err := strconv.ParseFloat(strings.TrimSuffix(t, "%"), 64)
			if err != nil {
				return rgba{}, false
			}
			if pct {
				x /= 100
				if i < 3 {
					x *= 255
				}
			}
			v[i] = x
		}
		return rgba{v[0], v[1], v[2], v[3]}, true
	}
	return rgba{}, false
}

func parseHex(h string) (rgba, bool) {
	if len(h) == 3 || len(h) == 4 {
		var d []byte
		for i := 0; i < len(h); i++ {
			d = append(d, h[i], h[i])
		}
		h = string(d)
	}
	if len(h) != 6 && len(h) != 8 {
		return rgba{}, false
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return rgba{}, false
	}
	if len(h) == 6 {
		return rgba{float64(n >> 16 & 255), float64(n >> 8 & 255), float64(n & 255), 1}, true
	}
	return rgba{float64(n >> 24 & 255), float64(n >> 16 & 255), float64(n >> 8 & 255), float64(n&255) / 255}, true
}

// over composites c on top of bg.
func (c rgba) over(bg rgba) rgba {
	a := c.a + bg.a*(1-c.a)
	if a == 0 {
		return rgba{}
	}
	mix := func(x, y float64) float64 { return (x*c.a + y*bg.a*(1-c.a)) / a }
	return rgba{mix(c.r, bg.r), mix(c.g, bg.g), mix(c.b, bg.b), a}
}

func (c rgba) luminance() float64 {
	lin := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

// contrast is the WCAG contrast ratio between two opaque colors.
func contrast(a, b rgba) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func distance(a, b rgba) float64 {
	return math.Sqrt((a.r-b.r)*(a.r-b.r) + (a.g-b.g)*(a.g-b.g) + (a.b-b.b)*(a.b-b.b))
}

// hsl returns hue in degrees, saturation and lightness in 0..1.
func (c rgba) hsl() (h, s, l float64) {
	r, g, b := c.r/255, c.g/255, c.b/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	d := mx - mn
	if d == 0 {
		return 0, 0, l
	}
	s = d / (1 - math.Abs(2*l-1))
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, l
}

func parsePx(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "px"))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}
