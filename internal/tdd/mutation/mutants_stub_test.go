package mutation

import (
	"context"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

type measuredCall = tddtest.MeasuredCall

func stubMutantsExec(t *testing.T, reply func(ctx context.Context, n int, c measuredCall) (int, error)) *[]measuredCall {
	t.Helper()
	return tddtest.StubMutantsExec(t, SetMutantsExecForTest, setMutantsListCountForTest, reply)
}
