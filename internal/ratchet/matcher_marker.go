package ratchet

import "strings"

// marker-within-lines: the one matcher kind whose window is an OWNERSHIP
// question — which declaration does this marker belong to — rather than a
// plain co-occurrence. Split out of matcher.go, which sits at module_size's
// ceiling.

func (l Law) markerHits(file string, raw, code []string) []Hit {
	var hits []Hit
	for i, line := range code {
		if l.excluded(line) || !l.Matcher.Trigger.MatchString(line) || l.escaped(file, raw, i) {
			continue
		}
		if l.markerAbove(raw, code, i) {
			continue
		}
		hits = append(hits, l.hit(file, i+1, strings.TrimSpace(raw[i])))
	}
	return hits
}

// markerAbove looks for the required marker on the trigger's own line, in the
// declaration's own comment block above it, and — for a `below`/`both` law —
// in the `lines` window under it.
func (l Law) markerAbove(raw, code []string, idx int) bool {
	match := func(line string) bool { return l.Matcher.Marker.MatchString(line) }
	if idx < len(raw) && match(raw[idx]) {
		return true
	}
	dir := l.Matcher.Direction
	if dir != DirectionBelow && l.markerInOwnBlock(raw, code, idx, match) {
		return true
	}
	return (dir == DirectionBelow || dir == DirectionBoth) &&
		l.scanRun(raw, idx, l.Matcher.Lines, 1, match)
}

// markerInOwnBlock walks up from the trigger to the declaration the marker
// belongs to and stops there: at the PREVIOUS TRIGGER line, or at the window
// edge — `lines` for an ordinary law, the contiguous comment run for a
// `contiguous` one, which declares that run AS its window and has no line cap
// to bound it. The previous-trigger stop is the whole of issue #652, where one
// marked field vouched for the three unmarked fields under it and each
// freshly-covered field's baseline row vanished on the next tightening, a
// baseline moving DOWN while nothing was marked: every declaration in that
// cascade is itself a trigger, so the stop alone ends it. A marker vouches for
// exactly one declaration: its own. It is tested BEFORE any comment-run
// consideration, because a marker is very often a CODE token — `rng_seed:`
// between two struct fields, a `cmd.Stderr = &buf` above the `.Output()` it
// feeds — and #658's run test ran first, halting the walk on those lines
// before ever matching them.
func (l Law) markerInOwnBlock(raw, code []string, idx int, match func(string) bool) bool {
	for i := idx - 1; i >= 0; i-- {
		if l.Contiguous && !inCommentRun(raw[i], l.commentPrefix()) {
			return false
		}
		if !l.Contiguous && i < idx-l.Matcher.Lines {
			return false
		}
		if l.carriesTrigger(code[i]) {
			return false
		}
		if match(raw[i]) {
			return true
		}
	}
	return false
}

// carriesTrigger reports whether a line the upward walk is stepping over is
// itself a trigger, and so owns the markers above it. It judges the line with
// its trailing comment stripped, because the question here is OWNERSHIP, not
// offence: an end-anchored trigger (subprocess_stderr_dropped's
// `\.Output\(\)\s*$`) cannot match a call that carries a trailing comment, so
// an escaped `cmd.Output() // stderr-ok: ...` was not seen as a trigger at
// all — the walk stepped straight past it and handed that call's own
// `cmd.Stderr` to an unrelated call in the function below (issue #667). An
// escape suppresses the HIT on the line it sits on; it never transfers
// ownership of the marker to a neighbour, so no escape test belongs here.
func (l Law) carriesTrigger(line string) bool {
	if l.Matcher.Trigger.MatchString(line) {
		return true
	}
	code, _ := splitTrailingComment(line, l.commentPrefix())
	return code != line && l.Matcher.Trigger.MatchString(code)
}
