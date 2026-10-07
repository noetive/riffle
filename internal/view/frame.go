package view

import (
	"net/url"
	"strings"
)

// frameLabel names an embedded document by its title, else by the host it
// loads from, so the agent knows what it cannot see inside. A frame with
// neither has no name: the view says only that its content is not shown, and
// does not make up words for the page.
func frameLabel(title, src string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	if u, err := url.Parse(src); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}
