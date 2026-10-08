package testcost

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type goEvent struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
}

// ParseGoJSON reads a `go test -json` stream. A top-level test is recorded as
// "<package>.<Test>" at its own elapsed; a subtest is not, its time being
// inside its parent's. The package-level event states the package's seconds. A
// stream cut off mid-line (a killed run) still yields what finished before it.
func ParseGoJSON(raw string) Run {
	var r Run
	dec := json.NewDecoder(strings.NewReader(raw))
	for {
		var e goEvent
		if dec.Decode(&e) != nil {
			return r
		}
		if e.Package == "" || e.Elapsed <= 0 || (e.Action != "pass" && e.Action != "fail") {
			continue
		}
		switch {
		case e.Test == "":
			r.Pkgs = set(r.Pkgs, e.Package, e.Elapsed)
		case !strings.Contains(e.Test, "/"):
			r.Tests = set(r.Tests, e.Package+"."+e.Test, e.Elapsed)
		}
	}
}

func set(m map[string]float64, k string, v float64) map[string]float64 {
	if m == nil {
		m = map[string]float64{}
	}
	m[k] = v
	return m
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// vitestFileRE is the default reporter's per-file line: a mark, an optional
// |project|, the file, a (N tests ...) summary and the file's time.
var vitestFileRE = regexp.MustCompile(`^\s*[✓✔❯×↓]\s+(?:\|[^|]*\|\s+)?(\S+\.(?:test|spec)\.\w+)\s+\(\d+ tests?[^)]*\)\s+(\d+(?:\.\d+)?)(ms|s)\b`)

// ParseVitestText reads vitest's default reporter. It states a time per file,
// not per test, so the rows are Pkgs.
func ParseVitestText(out string) Run {
	var r Run
	for _, line := range strings.Split(ansiRE.ReplaceAllString(out, ""), "\n") {
		m := vitestFileRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		v, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		if m[3] == "ms" {
			v /= 1000
		}
		r.Pkgs = set(r.Pkgs, m[1], math.Round(v*1000)/1000)
	}
	return r
}

var (
	cargoRunningRE  = regexp.MustCompile(`^\s*Running .*\(([^)]*)\)\s*$`)
	cargoDocRE      = regexp.MustCompile(`^\s*Doc-tests (\S+)`)
	cargoFinishedRE = regexp.MustCompile(`^test result: .*finished in (\d+(?:\.\d+)?)s`)
	cargoHashRE     = regexp.MustCompile(`-[0-9a-f]{16}(\.exe)?$`)
	nextestPassRE   = regexp.MustCompile(`^\s*(?:PASS|SLOW|FAIL) \[\s*(\d+(?:\.\d+)?)s\] (\S+) (\S+)\s*$`)
)

// ParseCargoText reads `cargo test`'s default output: the seconds of each test
// binary (the "finished in" of its result line, named by the binary the
// Running line names) and each doctest run, and nextest's per-test lines.
func ParseCargoText(out string) Run {
	var r Run
	name := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := cargoRunningRE.FindStringSubmatch(line); m != nil {
			path := strings.ReplaceAll(m[1], `\`, "/")
			name = cargoHashRE.ReplaceAllString(path[strings.LastIndex(path, "/")+1:], "")
			continue
		}
		if m := cargoDocRE.FindStringSubmatch(line); m != nil {
			name = "doc:" + m[1]
			continue
		}
		if m := cargoFinishedRE.FindStringSubmatch(line); m != nil && name != "" {
			v, _ := strconv.ParseFloat(m[1], 64)
			r.Pkgs = set(r.Pkgs, name, v)
			name = ""
			continue
		}
		if m := nextestPassRE.FindStringSubmatch(line); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			r.Tests = set(r.Tests, m[2]+" "+m[3], v)
		}
	}
	return r
}

// ParseOutput is the cost of one finished suite run: goJSON (the `go test
// -json` stream, "" when the run had none) and the runner's text output read
// by every parser here, each of which matches only its own runner's shape, and
// secs the run's own seconds. A runner whose output states no timing gives the
// seconds alone.
func ParseOutput(goJSON, output string, secs float64) Run {
	r := Merge(ParseGoJSON(goJSON), ParseVitestText(output), ParseCargoText(output))
	r.Secs = secs
	return r
}
