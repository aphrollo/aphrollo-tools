level: patch

A pre-merge run that is ended by a signal now says so on stderr, with the signal and the exit code, before it exits. A test binary ended this way used to leave `FAIL` with no failing test and no reason.
