package chromium

import "context"

// sameTabScript makes every new window load in this tab. The page is one
// tab; a window opened elsewhere would hide it and leave the session
// waiting on a page nobody is reading. Names that belong to a frame on the
// page are left alone.
const sameTabScript = `(() => {
  if (window.__rf_same_tab_3a9d) return;
  window.__rf_same_tab_3a9d = true;
  window.open = function (url) {
    if (url !== undefined && url !== null && String(url) !== '') {
      location.assign(new URL(String(url), location.href).href);
    }
    return null;
  };
  const owned = (name) => name === '_self' || name === '_top' || name === '_parent' ||
    (name !== '_blank' && !!window.frames[name]);
  const retarget = (e) => {
    const el = e.target && e.target.closest && e.target.closest('a[target],area[target],form[target]');
    if (el && !owned(el.getAttribute('target'))) el.setAttribute('target', '_self');
  };
  document.addEventListener('click', retarget, true);
  document.addEventListener('submit', retarget, true);
})();`

// installSameTab registers the script for every future document and for the
// current one.
func (p *Page) installSameTab(ctx context.Context) error {
	if _, err := p.call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": sameTabScript}); err != nil {
		return err
	}
	_, err := p.call(ctx, "Runtime.evaluate", map[string]any{"expression": sameTabScript})
	return err
}
