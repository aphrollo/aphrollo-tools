package a

func TestA_waits(t *testing.T) {
	time.Sleep(10 * time.Millisecond)
	select {
	case <-time.After(time.Second):
	}
}
