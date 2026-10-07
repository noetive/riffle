package view

import (
	"fmt"
	"slices"
	"strings"
)

// Delta is the change between two views as `+` added, `-` removed and `~`
// changed lines.
type Delta struct {
	Lines []string
}

// Empty reports whether nothing changed.
func (d Delta) Empty() bool { return len(d.Lines) == 0 }

// String renders the delta one line per change.
func (d Delta) String() string { return strings.Join(d.Lines, "\n") }

// Diff compares two views of the same page. Lines are matched by the backend
// identity of the node they describe, so a node that moves or is renumbered
// by a re-render is a change, not a removal and an addition. Lines without a
// node (the page header, counts) are matched by their text.
func Diff(old, new *View) Delta {
	oldLines, newLines := old.whole(), new.whole()
	oldByKey := map[int64]Line{}
	newByKey := map[int64]Line{}
	oldText := map[string]int{}
	newText := map[string]int{}
	for _, l := range oldLines {
		if l.Key != 0 {
			if _, dup := oldByKey[l.Key]; !dup {
				oldByKey[l.Key] = l
			}
		} else {
			oldText[l.Text()]++
		}
	}
	for _, l := range newLines {
		if l.Key != 0 {
			if _, dup := newByKey[l.Key]; !dup {
				newByKey[l.Key] = l
			}
		} else {
			newText[l.Text()]++
		}
	}
	var removed, added, changed []string
	for _, l := range oldLines {
		if l.Key != 0 {
			if _, ok := newByKey[l.Key]; !ok {
				removed = append(removed, "- "+identity(l))
			}
		} else if newText[l.Text()] < oldText[l.Text()] {
			oldText[l.Text()]--
			removed = append(removed, "- "+l.Text())
		}
	}
	for _, l := range newLines {
		if l.Key == 0 {
			if oldText[l.Text()] < newText[l.Text()] {
				newText[l.Text()]--
				added = append(added, "+ "+l.Text())
			}
			continue
		}
		o, ok := oldByKey[l.Key]
		switch {
		case !ok:
			added = append(added, "+ "+l.Text())
		case o.Label != l.Label || o.Body != l.Body:
			changed = append(changed, "~ "+l.Text())
		case !slices.Equal(o.Tags, l.Tags):
			changed = append(changed, "~ "+identity(l)+tagChanges(o.Tags, l.Tags))
		}
	}
	return Delta{Lines: append(append(removed, added...), changed...)}
}

// whole is the view's lines before the budget trimmed them.
func (v *View) whole() []Line {
	if v.Full != nil {
		return v.Full
	}
	return v.Lines
}

// Fit cuts the delta to a token budget, counted as a view's is, and says how
// many changes it left out. A budget of zero keeps every change. The first
// change is always kept.
func (d Delta) Fit(budget int) Delta {
	if budget <= 0 {
		return d
	}
	limit, used := budget*4, 0
	for i, l := range d.Lines {
		if i > 0 && used+len(l)+1 > limit {
			kept := append(append([]string(nil), d.Lines[:i]...),
				fmt.Sprintf("… %d more changes not shown; run view outline for the page", len(d.Lines)-i))
			return Delta{Lines: kept}
		}
		used += len(l) + 1
	}
	return d
}

// identity names a line in removals and tag changes: its ref when it has
// one, else its label and text.
func identity(l Line) string {
	if l.Ref != "" {
		return l.Ref
	}
	return strings.TrimSpace(l.Label + " " + l.Body)
}

func tagChanges(old, new []string) string {
	var sb strings.Builder
	for _, t := range new {
		if !slices.Contains(old, t) {
			sb.WriteString(" +" + t)
		}
	}
	for _, t := range old {
		if !slices.Contains(new, t) {
			sb.WriteString(" -" + t)
		}
	}
	return sb.String()
}
