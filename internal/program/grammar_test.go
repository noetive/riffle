package program

import (
	"os"
	"strings"
	"testing"
)

// The agent skill and the in-binary guide are one text; two copies would drift.
func TestSkillShippedWithTheInstallerIsTheGrammar(t *testing.T) {
	skill, err := os.ReadFile("../../installer/skills/riffle/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(skill) != grammar {
		t.Fatal("installer/skills/riffle/SKILL.md differs from internal/program/grammar.md; copy one over the other")
	}
}

// Every statement and every view the parser accepts is explained in the guide.
func TestGrammarMentionsEveryVerbAndView(t *testing.T) {
	g := Grammar()
	for _, v := range Verbs {
		if v == "replay" {
			continue // refused in this build, so not advertised
		}
		if !strings.Contains(g, "`"+v) {
			t.Errorf("guide does not explain %q", v)
		}
	}
	for _, view := range []string{"outline", "interactive", "read", "table", "find", "expand", "net", "unseen", "help"} {
		if !strings.Contains(g, "view "+view) && view != "outline" {
			t.Errorf("guide does not explain view %q", view)
		}
	}
	if strings.HasPrefix(g, "---") {
		t.Error("front matter must not reach the agent")
	}
}
