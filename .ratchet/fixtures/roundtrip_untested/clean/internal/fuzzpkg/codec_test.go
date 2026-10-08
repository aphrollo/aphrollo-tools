package fuzzpkg

import "testing"

func FuzzDecode(f *testing.F) { f.Fuzz(func(t *testing.T, s string) { _ = Decode(s) }) }
