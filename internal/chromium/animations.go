package chromium

import (
	"context"
	"errors"
	"time"
)

// finishAnimations jumps every finite transition and animation to its end
// state and returns how many it finished, so a panel that fades in or a toast
// that slides in is read where it comes to rest, without waiting out its
// duration. Endless animations, such as spinners, are left running.
const finishAnimations = `(() => {
  let n = 0;
  for (const a of document.getAnimations()) {
    const t = a.effect && a.effect.getComputedTiming();
    // One paused at rate zero cannot be finished; it stays where the page holds it.
    if (!t || !Number.isFinite(t.endTime) || a.playState === 'finished' || a.playbackRate === 0) continue;
    try { a.finish(); n++; } catch (e) { /* the page holds it where it is; leave it */ }
  }
  return n;
})()`

// maxAnimationRounds bounds how often a settle finishes animations that the
// end of earlier ones started: a page that chains them still ends.
const maxAnimationRounds = 3

// settleAnimations finishes the page's animations and lets the page hear that
// they ended. The browser sends animationend and transitionend when it next
// draws, and a handler that moves or restyles what animated must run before
// the page is read or clicked, not after.
func (p *Page) settleAnimations(ctx context.Context) error {
	for range maxAnimationRounds {
		n, err := p.Eval(ctx, finishAnimations)
		if err != nil || n == "0" {
			return err
		}
		if err := p.drawFrame(ctx); err != nil {
			return err
		}
	}
	return nil
}

// framePumpTimeout bounds the wait for a drawn frame. Drawing is best-effort:
// a page the browser will not draw for a while must not hold up the action.
var framePumpTimeout = 2 * time.Second

// drawFrame makes the browser draw one frame. A one-pixel capture is the
// cheapest way to make it draw.
func (p *Page) drawFrame(ctx context.Context) error {
	fctx, cancel := context.WithTimeout(ctx, framePumpTimeout)
	defer cancel()
	_, err := p.call(fctx, "Page.captureScreenshot", map[string]any{
		"format": "jpeg", "quality": 1,
		"clip": map[string]any{"x": 0, "y": 0, "width": 1, "height": 1, "scale": 1},
	})
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil // no frame this time; what waits on one hears on the next
	}
	return err
}
