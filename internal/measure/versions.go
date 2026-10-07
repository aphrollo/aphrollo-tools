package measure

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// UnknownVersion names the events written before records carried the binary's
// version.
const UnknownVersion = "unknown"

func binVersion(e tdd.Event) string {
	if e.BinVer == "" {
		return UnknownVersion
	}
	return e.BinVer
}

// VersionCount is one binary version a window spans and how many events it wrote.
type VersionCount struct {
	Version string `json:"version"`
	Events  int    `json:"events"`
}

// Versions lists the binary versions that wrote the events of the window (and
// lane), in the order each first appears in the log.
func Versions(events []tdd.Event, now time.Time, o Options) []VersionCount {
	s := newScope(events, now, o)
	var out []VersionCount
	index := map[string]int{}
	for _, e := range s.evs {
		if !s.in(e.at) {
			continue
		}
		v := binVersion(e.Event)
		i, ok := index[v]
		if !ok {
			i = len(out)
			index[v] = i
			out = append(out, VersionCount{Version: v})
		}
		out[i].Events++
	}
	return out
}

// VersionsText is the line that says which versions a window spans, "" for none.
func VersionsText(vs []VersionCount) string {
	if len(vs) == 0 {
		return ""
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		unit := "events"
		if v.Events == 1 {
			unit = "event"
		}
		parts[i] = fmt.Sprintf("%s (%d %s)", v.Version, v.Events, unit)
	}
	return "versions in this window: " + strings.Join(parts, ", ") + "\n"
}

// VersionSplit is the events one binary version is answerable for.
type VersionSplit struct {
	Version string      `json:"version"`
	Events  []tdd.Event `json:"-"`
}

// SplitByVersion partitions the log by binary version, oldest version first. A
// lane is never cut: all its events go with the version of its first event,
// since an arm assignment, its decisions and its merge only mean something
// together. An event with no lane keeps its own version.
func SplitByVersion(events []tdd.Event) []VersionSplit {
	s := newScope(events, time.Time{}, Options{})
	opened := map[string]string{}
	for _, e := range s.evs {
		if e.Lane == "" {
			continue
		}
		if _, ok := opened[e.Lane]; !ok {
			opened[e.Lane] = binVersion(e.Event)
		}
	}
	var out []VersionSplit
	index := map[string]int{}
	for _, e := range s.evs {
		v := binVersion(e.Event)
		if e.Lane != "" {
			v = opened[e.Lane]
		}
		i, ok := index[v]
		if !ok {
			i = len(out)
			index[v] = i
			out = append(out, VersionSplit{Version: v})
		}
		out[i].Events = append(out[i].Events, e.Event)
	}
	return out
}

// versionSet is a set of version names that lists them in a fixed order.
type versionSet map[string]bool

func (v versionSet) list() []string {
	out := make([]string, 0, len(v))
	for name := range v {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
