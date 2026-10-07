package chromium

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	json "github.com/goccy/go-json"
	"github.com/tidwall/gjson"
)

// clientHints asks the browser for the client hints it sends on its own, so
// the override can repeat them instead of dropping them.
const clientHints = `navigator.userAgentData.getHighEntropyValues(
  ["architecture", "bitness", "model", "platformVersion", "fullVersionList", "wow64", "formFactors"]
).then(JSON.stringify)`

// identify makes the page present the browser's own user agent followed by
// product, with the browser's own client hints. adopt extends it to the frames
// and workers the page starts.
func (p *Page) identify(ctx context.Context, base, product string) error {
	ua, err := productUserAgent(base, product)
	if err != nil {
		return err
	}
	hints, err := p.defaultClientHints(ctx)
	if err != nil {
		return fmt.Errorf("chromium: read client hints: %w", err)
	}
	if !gjson.Get(hints, "brands").IsArray() {
		return fmt.Errorf("chromium: the browser reported no client hints: %q", hints)
	}
	hintsJSON, err := withoutHeadlessBrand(hints)
	if err != nil {
		return err
	}
	override := map[string]any{"userAgent": ua, "userAgentMetadata": json.RawMessage(hintsJSON)}
	if _, err := p.call(ctx, "Network.setUserAgentOverride", override); err != nil {
		return err
	}
	p.override = override
	return nil
}

// onVersionPage holds once the throwaway tab shows chrome://version. Until its
// navigation commits the tab holds its initial about:blank document, which is
// not a secure context and so has no navigator.userAgentData.
const onVersionPage = `location.href.startsWith("chrome://version")`

// versionPageWait bounds how long the throwaway tab may take to show
// chrome://version, and versionPagePoll how often it is looked at.
var (
	versionPageWait = 10 * time.Second
	versionPagePoll = 20 * time.Millisecond
)

// defaultClientHints reads the client hints from a throwaway tab on a secure
// internal page: the page itself is on about:blank, which is not a secure
// context and so has no navigator.userAgentData.
func (p *Page) defaultClientHints(ctx context.Context) (string, error) {
	res, err := p.conn.call(ctx, "", "Target.createTarget", map[string]any{"url": "chrome://version"})
	if err != nil {
		return "", err
	}
	target := gjson.GetBytes(res, "targetId").String()
	defer func() { _, _ = p.conn.call(p.life, "", "Target.closeTarget", map[string]any{"targetId": target}) }()
	res, err = p.conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
	if err != nil {
		return "", err
	}
	session := gjson.GetBytes(res, "sessionId").String()
	if err := p.awaitVersionPage(ctx, session); err != nil {
		return "", err
	}
	res, err = p.conn.call(ctx, session, "Runtime.evaluate", map[string]any{
		"expression": clientHints, "returnByValue": true, "awaitPromise": true,
	})
	if err != nil {
		return "", err
	}
	if d := gjson.GetBytes(res, "exceptionDetails"); d.Exists() {
		return "", fmt.Errorf("%s", d.Get("exception.description").String())
	}
	return gjson.GetBytes(res, "result.value").String(), nil
}

// awaitVersionPage waits until the throwaway tab in session shows
// chrome://version. A look that fails while the document is being replaced
// is a look too early, not a failure; only running out of time is.
func (p *Page) awaitVersionPage(ctx context.Context, session string) error {
	// The bound holds for each look too, so one look that hangs cannot outlast it.
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, versionPageWait)
	defer cancel()
	deadline := time.Now().Add(versionPageWait)
	var last error
	for {
		res, err := p.conn.call(ctx, session, "Runtime.evaluate", map[string]any{
			"expression": onVersionPage, "returnByValue": true,
		})
		if err == nil && gjson.GetBytes(res, "result.value").Bool() {
			return nil
		}
		if err != nil {
			last = err
		}
		if parent.Err() != nil {
			return parent.Err()
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			if last != nil {
				return fmt.Errorf("chrome://version did not load within %s: %w", versionPageWait, last)
			}
			return fmt.Errorf("chrome://version did not load within %s", versionPageWait)
		}
		select {
		case <-ctx.Done():
			continue // the next pass says why
		case <-time.After(versionPagePoll):
		}
	}
}

// productUserAgent is the browser's user agent presented as an ordinary
// Chrome (the headless build's "HeadlessChrome" product token becomes
// "Chrome", the rest is byte-identical) followed by product.
func productUserAgent(base, product string) (string, error) {
	if base == "" {
		return "", errors.New("chromium: the browser reported no user agent to extend")
	}
	return strings.Replace(base, "HeadlessChrome/", "Chrome/", 1) + " " + product, nil
}

// withoutHeadlessBrand makes client hints agree with productUserAgent: the
// "HeadlessChrome" entry leaves brands and fullVersionList. Where no
// "Chromium" entry remains to name the engine it becomes "Chromium", with its
// version kept. It never claims "Google Chrome": a headless binary does not
// say whether it is the branded build, so only the engine is named. Hints that
// carry no such entry are returned as they came. Hints that do not parse are an
// error: sending them on would present a half-changed identity.
func withoutHeadlessBrand(hints string) (string, error) {
	const headless, engine = "HeadlessChrome", "Chromium"
	changed := false
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(hints), &top); err != nil {
		return "", fmt.Errorf("chromium: client hints are not a JSON object: %w", err)
	}
	for _, list := range []string{"brands", "fullVersionList"} {
		raw, ok := top[list]
		if !ok {
			continue
		}
		var entries []map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			return "", fmt.Errorf("chromium: client hints %s is not a list of brands: %w", list, err)
		}
		hasEngine := false
		for _, e := range entries {
			hasEngine = hasEngine || e["brand"] == engine
		}
		kept := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			if e["brand"] != headless {
				kept = append(kept, e)
				continue
			}
			changed = true
			if !hasEngine {
				e["brand"] = engine
				kept = append(kept, e)
			}
		}
		enc, err := json.Marshal(kept)
		if err != nil {
			return "", err
		}
		top[list] = enc
	}
	if !changed {
		return hints, nil
	}
	enc, err := json.Marshal(top)
	if err != nil {
		return "", err
	}
	return string(enc), nil
}
