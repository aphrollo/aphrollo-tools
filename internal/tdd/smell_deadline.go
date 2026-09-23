package tdd

import (
	"regexp"
	"strings"
)

// A timer is a WAIT when the goroutine has nothing else to wake it, and a
// BOUND when it is one arm of a select whose other arm carries the real
// answer. The two read identically to a regex and mean opposite things:
//
//	time.Sleep(200 * time.Millisecond)   // wait: costs 200ms every run
//	select {                             // bound: costs nothing unless the
//	case got = <-done:                   //        thing under test hangs, and
//	case <-time.After(30 * time.Second): //        turns that hang into a
//	        t.Fatal("never answered")    //        named failure
//	}
//
// Refusing the second is not a near-zero-false-positive block: seven of the
// twenty-one refusals `test-sleep` had ever made when issue #698 was filed
// were that exact shape, and every one ended in the author respelling the
// same real-time deadline as `context.WithTimeout` + `case <-ctx.Done()` —
// which the regex does not know and therefore allows. One gave up and deleted
// the watchdog, leaving a hang where a named failure had been. A rule whose
// only effect is to pick between two spellings of one deadline, and whose
// worst outcome is the removal of a safety net, is refusing correct work.

// deadlineArmRe matches a select case arm that receives from time.After. It is
// anchored at the head of the line so only an arm — never a bare `<-time.After`
// receive, and never an assignment like `timeout := time.After(d)` — can reach
// the exemption below.
var deadlineArmRe = regexp.MustCompile(`^\s*case\s+<-\s*time\.After\s*\(`)

// selectOpenRe matches the line that opens a select statement, which is where
// the arm count for a timer arm starts.
var selectOpenRe = regexp.MustCompile(`\bselect\s*\{`)

// selectArmRe matches a case/default arm at the head of a line.
var selectArmRe = regexp.MustCompile(`^\s*(?:case\b|default\b)`)

// hasRealTimeWait reports a real-time sleep among the judged lines. It is
// sleepRe line by line, minus the one shape sleepRe reads backwards: a
// `case <-time.After(...)` arm of a select that has another arm.
//
// The exemption is deliberately grudging. A timer arm is admitted only when
// the enclosing select is FOUND and carries a second arm, so a one-armed
// `select { case <-time.After(d): }` — a sleep wearing a select — still trips,
// and so does an arm whose select cannot be located at all. Anything other
// than proof that this timer bounds another wait leaves the line refused.
func hasRealTimeWait(v view) bool {
	var context []string
	for _, line := range strings.Split(v.code, "\n") {
		if !sleepRe.MatchString(line) {
			continue
		}
		if !deadlineArmRe.MatchString(line) {
			return true
		}
		if context == nil {
			context = strings.Split(wholeOf(v), "\n")
		}
		if !boundsAnotherWait(context, line) {
			return true
		}
	}
	return false
}

// wholeOf is the file-wide masked code a policy reads for context. The judged
// slice is only the lines an edit ADDS, so an edit that appends a timer arm to
// a select that already exists offers one line and no block around it — the
// select has to come from the whole post-image or the answer is wrong.
// A view built without one (the zero value) falls back to its own code, which
// for every whole-file caller is the same string.
func wholeOf(v view) string {
	if v.whole != "" {
		return v.whole
	}
	return v.code
}

// boundsAnotherWait reports whether EVERY place the given timer-arm line
// appears in the file sits in a select with at least one other arm. All of
// them, not any: the same arm text can appear more than once (three identical
// `case <-time.After(precommitTestTimeout):` arms in one test is real code
// from this repo), and one of them being a bare wait makes the edit a sleep.
func boundsAnotherWait(lines []string, arm string) bool {
	want, found := strings.TrimSpace(arm), false
	for i, line := range lines {
		if strings.TrimSpace(line) != want {
			continue
		}
		found = true
		if selectArms(lines, i) < 2 {
			return false
		}
	}
	return found
}

// selectArms counts the arms of the select statement that directly encloses
// line i, or 0 when the enclosing block is not a select (or cannot be found).
// Arms are counted at the select's own brace depth, so a switch or a nested
// select inside an arm's body contributes nothing — over-counting would admit
// a real sleep, which is the error this check cannot afford.
func selectArms(lines []string, i int) int {
	start, up := -1, 0
	for j := i - 1; j >= 0; j-- {
		up -= braceDelta(lines[j])
		if up < 0 { // this line opened the block line i lives in
			if !selectOpenRe.MatchString(lines[j]) {
				return 0
			}
			start = j
			break
		}
	}
	if start == -1 { // no enclosing block opener above line i at all
		return 0
	}
	arms := 0
	depth := braceDelta(lines[start])
	for j := start + 1; j < len(lines) && depth > 0; j++ {
		if depth == 1 && selectArmRe.MatchString(lines[j]) {
			arms++
		}
		depth += braceDelta(lines[j])
	}
	return arms
}

// braceDelta is how much a line opens (positive) or closes (negative) the
// block nesting. Braces inside strings and comments are already blanked by the
// masker, so only structural ones are counted.
func braceDelta(line string) int {
	return strings.Count(line, "{") - strings.Count(line, "}")
}
