//go:build windows

package lock

import (
	"testing"

	"golang.org/x/sys/windows"
)

// TestFiletimeTicks_CombinesHighAndLowIntoA64BitTickCount pins the FILETIME
// bit layout itself (high dword shifted 32, low dword below it) against a
// literal, closed-form expectation — never computed by calling the
// function under test.
func TestFiletimeTicks_CombinesHighAndLowIntoA64BitTickCount(t *testing.T) {
	ft := windows.Filetime{LowDateTime: 0x00000001, HighDateTime: 0x00000002}
	got := filetimeTicks(ft)
	want := uint64(0x0000000200000001)
	if got != want {
		t.Errorf("filetimeTicks(%+v) = %#x, want %#x", ft, got, want)
	}
}

// TestTicksToSeconds_ConvertsHundredNanosecondUnitsToSeconds pins the FILETIME
// unit definition itself — 10,000,000 ticks of 100ns each is exactly one
// second, a fact from the Windows API contract, not a value this test or
// the code under test invented.
func TestTicksToSeconds_ConvertsHundredNanosecondUnitsToSeconds(t *testing.T) {
	got := ticksToSeconds(10_000_000)
	if got != 1.0 {
		t.Errorf("ticksToSeconds(10_000_000) = %v, want 1.0", got)
	}
}

// TestTicksToSeconds_HalfASecond pins the same conversion at a
// non-round input, so a mutant that dropped a factor of 2 or swapped
// multiply for divide would not slip past the exact-second case above.
func TestTicksToSeconds_HalfASecond(t *testing.T) {
	got := ticksToSeconds(5_000_000)
	if got != 0.5 {
		t.Errorf("ticksToSeconds(5_000_000) = %v, want 0.5", got)
	}
}
