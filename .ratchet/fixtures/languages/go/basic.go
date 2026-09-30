package a

func TestFoo(t *testing.T)      {}
func TestBar(t *testing.T)      {}
func TestMain(m *testing.M)     {}
func BenchmarkBaz(b *testing.B) {}
func helper()                   {}

var x = f() //nolint:errcheck
