package a

func TestA_failsWith(t *testing.T) {
	require.Error(t, err)
	require.ErrorIs(t, err, ErrGone)
	require.Error(t, other) // any-error-ok: a boundary test of occurrence
}
