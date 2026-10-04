package workspace

// ratchet: test_removed internal/workspace/enqueue_detect_test.go: the merge-queue detection, the queue entry and removal parsers and the enqueue argv moved into the GitHub adapter with the code they test; internal/integrate/host/github/queue_read_test.go holds each of those tests, unchanged in what they assert.
// ratchet: test_removed internal/workspace/jobsteps_test.go: the job step count moved into the GitHub adapter; TestJobSteps_ReadsTheCountGhPrintsAndNamesAFailure in internal/integrate/host/github/queue_read_test.go holds it.

// ratchet: test_removed TestMarkNotStarted_FlagsFailedActionsJobsWithNoSteps: markNotStarted moved into the GitHub adapter; the same test lives in internal/integrate/host/github/queue_read_test.go.
// ratchet: test_removed TestMergeBodyArgs_MatchTheJudgedHead: the merge argv moved into the GitHub adapter; the same test lives in internal/integrate/host/github/queue_read_test.go.

// ratchet: test_removed TestGhAPIFindPR_NoneFound: the REST pull reads moved into the GitHub adapter; internal/integrate/host/github/pr_read_test.go holds TestFindPR_NoneFoundIsNotFoundAndAPRIsItsNumber.
// ratchet: test_removed TestGhAPIFindPR_Found: moved with it, same test.
// ratchet: test_removed TestGhAPIFindPR_LooksUpWithGETNotACreatePOST: moved to TestFindPR_LooksUpWithGETNotACreatePOST in internal/integrate/host/github/pr_read_test.go.
// ratchet: test_removed TestGhAPIGetPR_ParsesFieldsIncludingUnknownMergeable: moved to TestPR_ReadsTheFieldsIncludingAnUnknownMergeable in internal/integrate/host/github/pr_read_test.go.
// ratchet: test_removed TestGhAPIGetPR_MergedAndClean: moved to TestPR_MergedBeatsClosedAndCleanIsMergeable in internal/integrate/host/github/pr_read_test.go.
// ratchet: test_removed TestGhAPIViewByBranch_AbsenceIsNilNil: moved to TestPRByBranch_AbsenceIsNilNilAndANumericRefFetchesDirectly in internal/integrate/host/github/pr_read_test.go.
// ratchet: test_removed TestGhAPIViewByRef_NumericRefFetchesDirectly: moved with it, same test.
// ratchet: test_removed TestGhReadyPRSandboxFallback_PropagatesAFindError: moved to TestMarkReady_AFailedLookupInTheSandboxFallbackIsPropagated in internal/integrate/host/github/pr_read_test.go.

// ratchet: test_removed TestGhCombinedOutput_ReturnsStdoutOnlyEvenWithAStderrWarning: the gh transport moved into the GitHub adapter; TestExecRunner_ReturnsStdoutOnlyEvenWithAStderrWarning in internal/integrate/host/github/transport_test.go holds it.
// ratchet: test_removed TestGhOutput_GivesUpOnAStalledCallAtTheDeadline: the gh deadline moved into the GitHub adapter; TestExecRunner_AnAbsurdlyShortDeadlineNamesTheStalledCall in internal/integrate/host/github/replay_test.go holds it.

// ratchet: test_removed TestRulesHaveMergeQueue_LooksForTheQueueRuleByType: moved to TestRulesHaveMergeQueue_LooksForTheQueueRuleByTypeAcrossPages in internal/integrate/host/github/queue_read_test.go, with the page cases.
// ratchet: test_removed TestRulesHaveMergeQueue_ReadsEveryPageOfASlurpedRead: the "slurped page two" case of TestRulesHaveMergeQueue_LooksForTheQueueRuleByTypeAcrossPages in internal/integrate/host/github/queue_read_test.go.
// ratchet: test_removed TestRulesHaveMergeQueue_ReadsConcatenatedPages: the "back to back" cases of TestRulesHaveMergeQueue_LooksForTheQueueRuleByTypeAcrossPages in internal/integrate/host/github/queue_read_test.go.
// ratchet: test_removed TestRulesReadResult_A404IsNoQueueAndEverythingElseIsAnError: folded into TestRulesReadResult_APlanThatLacksRulesetsIsNoQueueButATokenProblemIsNot and TestRulesReadResult_A404MeansNoRulesetsAndAnythingElseIsAnErrorWithTheFix in internal/integrate/host/github.
