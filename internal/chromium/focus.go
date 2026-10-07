package chromium

// deepActiveElement is the element that holds keyboard focus, looking through
// open shadow roots: document.activeElement stops at the host of a component.
const deepActiveElement = `(() => {
  let e = document.activeElement;
  while (e && e.shadowRoot && e.shadowRoot.activeElement) e = e.shadowRoot.activeElement;
  return e;
})()`
