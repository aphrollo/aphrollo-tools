package a

func TestA_bounded(t *testing.T) {
	s := "time.Sleep(1)"
	// time.Sleep(1) is only named here
	time.Sleep(time.Second) // real-time: the retry backoff is the behavior under test
	time.Sleep(time.Second) // real-time:
	select {
	case got := <-done:
		_ = got
	case <-time.After(time.Second):
		t.Fatal("never answered")
	}
}
