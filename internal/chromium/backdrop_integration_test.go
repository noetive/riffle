//go:build integration

package chromium_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/chromium"
	"github.com/noetive/riffle/internal/snapshot"
)

const backdropPage = `<!doctype html><html><body style="background:#000;margin:0">
<div id="card" style="background-image:linear-gradient(#fff,#eee);color:#000;height:40px"><span>gradient words</span><b>more</b></div>
<div id="veil" style="background:rgba(0,0,0,0.3);color:rgb(125,125,125)">veil words</div>
</body></html>`

// The snapshot carries a gradient as background-image, and a text node carries
// its parent's computed style, background colour included.
func TestSnapshotCarriesBackgroundImageAndTextInheritsParentStyle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(backdropPage)) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Load(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	s, err := p.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var card, veilText = snapshot.None, snapshot.None
	for i := int32(0); i < int32(s.Len()); i++ {
		if id, _ := s.Attr(i, "id"); s.Kind[i] == snapshot.KindElement && id == "card" {
			card = i
		}
		if s.Kind[i] == snapshot.KindText && strings.Contains(s.Text[i], "veil words") {
			veilText = i
		}
	}
	if card == snapshot.None || veilText == snapshot.None {
		t.Fatal("fixture nodes missing")
	}
	if img := s.Prop(card, snapshot.BackgroundImage); !strings.Contains(img, "linear-gradient") {
		t.Errorf("background-image = %q", img)
	}
	if bg := s.Style[veilText][snapshot.BackgroundColor]; !strings.Contains(bg, "0.3") {
		t.Errorf("text node background-color = %q, want its parent's translucent layer", bg)
	}
}
