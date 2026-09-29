package shell

import (
	"reflect"
	"testing"
)

func groupTexts(cmd string, openDepth int) (texts [][]string, opened, closed int) {
	for _, seg := range shellSegmentsTokens(cmd) {
		inner, o, c := unwrapGroups(seg, openDepth)
		if len(inner) > 0 {
			texts = append(texts, wordTexts(inner))
		}
		opened += o
		closed += c
		openDepth += o - c
	}
	return texts, opened, closed
}

func TestUnwrapGroups_SubshellOpenAndCloseRideOnTheEdgeWords(t *testing.T) {
	for _, tc := range []struct {
		name           string
		cmd            string
		texts          [][]string
		opened, closed int
	}{
		{"subshell", "(cd dir && go test ./...)", [][]string{{"cd", "dir"}, {"go", "test", "./..."}}, 1, 1},
		{"spaced open", "( cd dir; go test )", [][]string{{"cd", "dir"}, {"go", "test"}}, 1, 1},
		{"lone close", "(cd dir; go test; )", [][]string{{"cd", "dir"}, {"go", "test"}}, 1, 1},
		{"brace group is dropped, not counted", "{ cd dir; go test; }", [][]string{{"cd", "dir"}, {"go", "test"}}, 0, 0},
		{"command substitution operand", "go test -C $(pwd) ./...", [][]string{{"go", "test", "-C", "$(pwd)", "./..."}}, 0, 0},
		{"stray close without an open group", "go test x)", [][]string{{"go", "test", "x)"}}, 0, 0},
		{"lone open paren", "(", nil, 1, 0},
		{"unclosed command substitution operand", "(go test $(pwd)", [][]string{{"go", "test", "$(pwd)"}}, 1, 0},
		{"nested", "((cd a); go test)", [][]string{{"cd", "a"}, {"go", "test"}}, 2, 2},
	} {
		texts, opened, closed := groupTexts(tc.cmd, 0)
		if !reflect.DeepEqual(texts, tc.texts) || opened != tc.opened || closed != tc.closed {
			t.Errorf("%s: %q = %q opened %d closed %d, want %q opened %d closed %d",
				tc.name, tc.cmd, texts, opened, closed, tc.texts, tc.opened, tc.closed)
		}
	}
}

func TestUnwrapGroups_CloseNeedsAnOpenGroup(t *testing.T) {
	seg := shellSegmentsTokens("go test)")[0]
	if inner, _, closed := unwrapGroups(seg, 0); closed != 0 || !reflect.DeepEqual(wordTexts(inner), []string{"go", "test)"}) {
		t.Errorf("depth 0: %q closed %d, want the word left alone", wordTexts(inner), closed)
	}
	if inner, _, closed := unwrapGroups(seg, 1); closed != 1 || !reflect.DeepEqual(wordTexts(inner), []string{"go", "test"}) {
		t.Errorf("depth 1: %q closed %d, want one close", wordTexts(inner), closed)
	}
}
