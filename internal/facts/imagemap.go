package facts

import (
	"strconv"
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

// placeImageMapRegions gives each <area> of a client-side image map the box of
// its region on the image that uses the map. The browser lays out no box for
// an area, yet a person clicks it where it is drawn, so without this the
// links of an image map are hidden.
func placeImageMapRegions(s *snapshot.Snapshot) {
	maps := map[string]int32{}
	for i := int32(0); i < int32(s.Len()); i++ {
		if s.Kind[i] != snapshot.KindElement || s.Tag[i] != "map" {
			continue
		}
		for _, attr := range []string{"name", "id"} {
			if v, ok := s.Attr(i, attr); ok && v != "" {
				if _, dup := maps[v]; !dup {
					maps[v] = i
				}
			}
		}
	}
	if len(maps) == 0 {
		return
	}
	for i := int32(0); i < int32(s.Len()); i++ {
		if s.Kind[i] != snapshot.KindElement || s.Tag[i] != "img" || !s.Laid[i] {
			continue
		}
		use, ok := s.Attr(i, "usemap")
		if !ok {
			continue
		}
		m, ok := maps[strings.TrimPrefix(use, "#")]
		if !ok {
			continue
		}
		for _, a := range s.Children[m] {
			if s.Kind[a] != snapshot.KindElement || s.Tag[a] != "area" || s.Laid[a] {
				continue
			}
			if _, ok := s.Attr(a, "href"); !ok {
				continue
			}
			s.Laid[a] = true
			s.Box[a] = areaBox(s, a, s.Box[i])
			s.Paint[a] = s.Paint[i]
		}
	}
}

// areaBox is the bounding box of an area's shape, in page coordinates, with
// the image at img. A shape that cannot be read is the whole image.
func areaBox(s *snapshot.Snapshot, area int32, img snapshot.Rect) snapshot.Rect {
	shape, _ := s.Attr(area, "shape")
	raw, _ := s.Attr(area, "coords")
	var n []float64
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return img
		}
		n = append(n, v)
	}
	minX, minY, maxX, maxY := 0.0, 0.0, 0.0, 0.0
	switch strings.ToLower(shape) {
	case "circle", "circ":
		if len(n) < 3 {
			return img
		}
		minX, minY, maxX, maxY = n[0]-n[2], n[1]-n[2], n[0]+n[2], n[1]+n[2]
	case "poly", "polygon":
		if len(n) < 4 {
			return img
		}
		minX, minY, maxX, maxY = n[0], n[1], n[0], n[1]
		for k := 0; k+1 < len(n); k += 2 {
			minX, maxX = min(minX, n[k]), max(maxX, n[k])
			minY, maxY = min(minY, n[k+1]), max(maxY, n[k+1])
		}
	case "", "rect", "rectangle":
		if len(n) < 4 {
			return img
		}
		minX, minY, maxX, maxY = min(n[0], n[2]), min(n[1], n[3]), max(n[0], n[2]), max(n[1], n[3])
	default:
		return img
	}
	return snapshot.Rect{X: img.X + minX, Y: img.Y + minY, W: maxX - minX, H: maxY - minY}
}
