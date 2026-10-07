package chromium

import (
	"context"
	"strings"
)

// invalidPrefix marks the console line the validation script writes, so the
// page's own output is never mistaken for a report.
const invalidPrefix = "__rf_invalid__"

// validationScript reports every field the browser refuses on submit. Chrome
// shows the message in a bubble no snapshot can see, so without this a form
// that does not submit gives the agent nothing to go on.
const validationScript = `(() => {
  if (window.__rf_invalid_5d2b) return;
  window.__rf_invalid_5d2b = true;
  const nameOf = (el) => {
    const l = el.labels && el.labels[0];
    return (el.getAttribute('aria-label') || (l && l.textContent) || el.placeholder || el.name || el.id || el.tagName.toLowerCase()).trim().replace(/\s+/g, ' ');
  };
  document.addEventListener('invalid', (e) => {
    console.debug('` + invalidPrefix + `' + nameOf(e.target) + ': ' + e.target.validationMessage);
  }, true);
})();`

func (p *Page) installValidation(ctx context.Context) error {
	if _, err := p.call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": validationScript}); err != nil {
		return err
	}
	_, err := p.call(ctx, "Runtime.evaluate", map[string]any{"expression": validationScript})
	return err
}

// invalidReport returns the message of a validation console line, if it is one.
func invalidReport(line string) (string, bool) {
	return strings.CutPrefix(line, invalidPrefix)
}
