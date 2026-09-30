package a

import "testing"

func TestFoo(t *testing.T)      {}
func TestBar(t *testing.T)      {}
func TestMain(m *testing.M)     {}
func BenchmarkBaz(b *testing.B) {}
func helper()                   {}

func f() error { return nil }

var x = f() //nolint:errcheck
