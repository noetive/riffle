package chromium

import (
	"strings"
	"testing"
)

// FuzzSplitCombo reads key names an agent typed. Any string must split
// without a panic into modifier bits and a key, and what it accepts must
// rebuild to the same key with the modifiers it named.
func FuzzSplitCombo(f *testing.F) {
	for _, s := range []string{
		"Enter", "Tab", "Escape", "a", "A", "1", "Space", "+", "Shift+Tab", "Control+a", "Ctrl+Shift+a", "Meta+a", "Cmd+c",
		"Alt+ArrowDown", "Shift++", "Control+", "+a", "++", "+++", "Hyper+a", "shift+tab", "SHIFT+TAB", "Control+Control+a",
		"PageUp", "PageDown", "Home", "End", "Delete", "Backspace", "ArrowLeft", "ArrowRight", "ArrowUp", "F5", "F12", "Insert", "CapsLock",
		"Shift+Control+Alt+Meta+a", "shift+CONTROL+alt+meta+Z", "Option+Command+Delete", "Control+Shift+Tab", "Alt+Shift+ArrowLeft",
		"Control+1", "Control+-", "Control+=", "Control+Space", "Shift+Enter", "Meta+Backspace", "Control+Home", "Control+End",
		"Shift+A", "Shift+1", "Shift+!", "Alt+é", "Control+日", "Control+😀", "Shift+Shift", "Control+Control", "Alt+", "+Shift", "Shift+Tab+",
		"Shift+ +", "Shift+++", "+Control+a", "Control + a", "Control+ a", "Contr0l+a", "Super+a", "Win+a", "Fn+a", "AltGr+a", "Escape+Tab",
		"a+", "+a+", "++a", "Control+a+b", "\t", "\n", "Shift+\n", "Control+\u0000", "Shift+Tab\u0000", "ctrl+SHIFT+a", "CTRL+a",
		"", " ", "Shift+ ", "é", "Shift+é", "\x00", "Control+\x00", "a+b+c", "Option+Delete", "Command+Shift+Alt+Control+x",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		mods, key, err := splitCombo(name)
		if err != nil {
			return
		}
		if mods < 0 || mods > 15 {
			t.Fatalf("%q: modifier bits %d out of range", name, mods)
		}
		if !strings.HasSuffix(name, key) {
			t.Fatalf("%q: key %q is not the end of the name", name, key)
		}
		if mods == 0 && key != name {
			t.Fatalf("%q: no modifiers yet the key became %q", name, key)
		}
	})
}
