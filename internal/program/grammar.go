package program

import (
	_ "embed"
	"strings"
)

//go:embed grammar.md
var grammar string

// Grammar is the guide to the program language, as served by `view help` and
// shipped as the agent skill. The skill file under installer/ is a copy of
// grammar.md; a test keeps the two identical.
func Grammar() string {
	body := grammar
	if strings.HasPrefix(body, "---\n") {
		if _, rest, ok := strings.Cut(body[4:], "\n---\n"); ok {
			body = rest
		}
	}
	return strings.TrimSpace(body)
}
