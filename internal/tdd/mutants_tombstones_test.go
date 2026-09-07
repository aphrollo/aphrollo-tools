package tdd

// The mutation receipt, the outcome store and the detached run that produced
// them are deleted. Every test that measured one of them is buried here,
// grouped by the file it stood in, with the reason it is gone and — where the
// claim survives in another shape — the test that now makes it.
//
// The stage these tests covered refused 150 merges between 2026-08-15 and
// 2026-09-07; 141 of them logged no reason at all, and the word survivor
// appears zero times in the whole log.

// --- internal/tdd/autoescape_missingreceipt_test.go
// ratchet: test_removed TestIsReceiptRejection_RecognisesTheMissingReceiptRefusalToo: the missing-receipt escape candidate is deleted with the receipt stage
// ratchet: test_removed TestIsReceiptRejection_IsStillFalseForAStageRejection: the missing-receipt escape candidate is deleted with the receipt stage

// --- internal/tdd/autoescape_test.go
// ratchet: test_removed TestTheSurvivorPredicateMatchesTheReceiptGatesOwnRejection: the receipt-family escape predicate is deleted; the survivor trigger now reads the mutation stage's own refusal, proved by TestTheSurvivorPredicate_MatchesTheMutationStagesOwnRefusal
// ratchet: test_removed TestAMissingReceiptRejectionIsNotAnEscape: the receipt-family escape predicate is deleted; the survivor trigger now reads the mutation stage's own refusal, proved by TestTheSurvivorPredicate_MatchesTheMutationStagesOwnRefusal
// ratchet: test_removed TestTheReceiptPredicateMatchesTheReceiptGatesOwnRejections: the receipt-family escape predicate is deleted; the survivor trigger now reads the mutation stage's own refusal, proved by TestTheSurvivorPredicate_MatchesTheMutationStagesOwnRefusal

// --- internal/tdd/mechanical_receipt_waiver_test.go
// ratchet: test_removed TestMechanical_CatchUpMergeStillRunsTheStagesAndSuites: the receipt stage and its waivers are deleted from Mechanical
// ratchet: test_removed TestMechanical_CIJudgedRepoStillRunsTheStages: the receipt stage and its waivers are deleted from Mechanical
// ratchet: test_removed TestMechanical_ReceiptRefusalStillShortCircuits: the receipt stage and its waivers are deleted from Mechanical
// ratchet: test_removed TestMechanical_CatchUpMergeStillRefusesOnARatchetHit: the receipt stage and its waivers are deleted from Mechanical

// --- internal/tdd/mutants_accept_kind_test.go
// ratchet: test_removed TestGoMutantsReceipt_CountsAcceptedSurvivorsByKindSeparately: renamed for the function it actually calls, splitAcceptedSurvivors; the assertions are unchanged

// --- internal/tdd/mutants_accepted_timeout_test.go
// ratchet: test_removed TestGoMutantsReceipt_AcceptsATimedOutMutantThatIsOnTheAcceptList: the receipt's accepted-timeout bookkeeping is deleted; judgeMutants reports an unmeasured mutant and refuses, proved by TestJudgeMutants_RefusesAMutantThatStayedUnmeasured
// ratchet: test_removed TestGoMutantsReceipt_StillCountsATimedOutMutantNobodyAccepted: the receipt's accepted-timeout bookkeeping is deleted; judgeMutants reports an unmeasured mutant and refuses, proved by TestJudgeMutants_RefusesAMutantThatStayedUnmeasured
// ratchet: test_removed TestGoMutantsReceipt_IgnoresAnAcceptEntryWithNoReasonForATimeout: the receipt's accepted-timeout bookkeeping is deleted; judgeMutants reports an unmeasured mutant and refuses, proved by TestJudgeMutants_RefusesAMutantThatStayedUnmeasured
// ratchet: test_removed TestGoMutantsReceipt_NamesAnAcceptedTimeoutSoACarryCanSeeTheDecision: the receipt's accepted-timeout bookkeeping is deleted; judgeMutants reports an unmeasured mutant and refuses, proved by TestJudgeMutants_RefusesAMutantThatStayedUnmeasured
// ratchet: test_removed TestRecountReceipt_KeepsATimedOutMutantTheProducerAccepted: the receipt's accepted-timeout bookkeeping is deleted; judgeMutants reports an unmeasured mutant and refuses, proved by TestJudgeMutants_RefusesAMutantThatStayedUnmeasured
// ratchet: test_removed TestRecountReceipt_StillCountsATimedOutMutantTheProducerDidNotAccept: the receipt's accepted-timeout bookkeeping is deleted; judgeMutants reports an unmeasured mutant and refuses, proved by TestJudgeMutants_RefusesAMutantThatStayedUnmeasured

// --- internal/tdd/mutants_adopt_test.go
// ratchet: test_removed TestAdoptCarriedOutcomes_ACarriedSurvivorStillBlocksTheMerge: adopting carried outcomes is deleted with the outcome store
// ratchet: test_removed TestAdoptCarriedOutcomes_KeepsAnAcceptedSurvivorAccepted: adopting carried outcomes is deleted with the outcome store
// ratchet: test_removed TestAdoptCarriedOutcomes_ACarriedTimeoutIsStillATimeout: adopting carried outcomes is deleted with the outcome store
// ratchet: test_removed TestAdoptCarriedOutcomes_CountsDescribeTheMergedSet: adopting carried outcomes is deleted with the outcome store

// --- internal/tdd/mutants_argswarning_test.go
// ratchet: test_removed TestMutantsArgsUnreadWarning_EmptyWhenProducerOutputEchoesTheArgs: the producer's APHROLLO_MUTANTS_ARGS handshake is deleted; the binary passes the flags to the tool itself
// ratchet: test_removed TestMutantsArgsUnreadWarning_NamesTheVarWhenProducerOutputShowsNoTrace: the producer's APHROLLO_MUTANTS_ARGS handshake is deleted; the binary passes the flags to the tool itself
// ratchet: test_removed TestMutantsArgsUnreadWarning_EmptyWhenNothingWasComputed: the producer's APHROLLO_MUTANTS_ARGS handshake is deleted; the binary passes the flags to the tool itself
// ratchet: test_removed TestMutantsEnvValue_ReadsBackTheComputedArgs: the producer's APHROLLO_MUTANTS_ARGS handshake is deleted; the binary passes the flags to the tool itself
// ratchet: test_removed TestMutantsEnvValue_EmptyWhenKeyAbsent: the producer's APHROLLO_MUTANTS_ARGS handshake is deleted; the binary passes the flags to the tool itself
// ratchet: test_removed TestMutantsOutputCapture_CapsStoredBytesButNeverShortWrites: the producer's APHROLLO_MUTANTS_ARGS handshake is deleted; the binary passes the flags to the tool itself

// --- internal/tdd/mutants_audit_test.go
// ratchet: test_removed TestMutantsAudit_SourceNeverReferencesTheReceiptMachinery: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestRunMutantsAudit_WritesNoFileToTheReceiptStore: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestRunMutantsAudit_RefusesAnEmptyScope: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestRunMutantsAudit_RefusesOutsideAGitRepository: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestRunMutantsAudit_RefusesARepoWithNeitherManifest: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestRunMutantsAudit_RefusesWhenTheDriveCannotFitTheBuild: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestRunMutantsAudit_DispatchesToRustAndRendersRankedSurvivors: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestRunMutantsAudit_DispatchesToGoForAGoModRepo: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestRunMutantsAudit_ReportsADriverFailure: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read
// ratchet: test_removed TestOutcomesToReport_RanksSurvivorsByFileThenLine: `gate mutants audit` is deleted: it measured a whole crate outside any lane and wrote no verdict anything read

// --- internal/tdd/mutants_baseline_scope_test.go
// ratchet: test_removed TestMutantsTouchedPackages_NamesOnlyThePackageOwningTheDiffFile: the package scoping is computed by measureDiff now, proved by TestMeasureDiff_ScopesToCrateSourcesAtMergeBase
// ratchet: test_removed TestMutantsTouchedPackages_NamesEveryPackageADiffFileTouches: the package scoping is computed by measureDiff now, proved by TestMeasureDiff_ScopesToCrateSourcesAtMergeBase
// ratchet: test_removed TestMutantsTouchedPackages_NilOnUnreadableDiff: the package scoping is computed by measureDiff now, proved by TestMeasureDiff_ScopesToCrateSourcesAtMergeBase
// ratchet: test_removed TestMutantsTouchedPackages_NilWhenNoTouchedFileIsCargoOwned: the package scoping is computed by measureDiff now, proved by TestMeasureDiff_ScopesToCrateSourcesAtMergeBase

// --- internal/tdd/mutants_carry_reason_test.go
// ratchet: test_removed TestPlanMutants_SaysTheBlobMovedWhenTheFileItselfChanged: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestPlanMutants_SaysTheFenceMovedWhenOnlyThePackagesTestsChanged: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestPlanMutants_SaysUnmeasuredRatherThanBlamingABlobThatNeverHadAVerdict: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestPlanMutants_RecordsNoReasonForAMutantThatCarried: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestCarrySkipSummary_IsOneLinePerFileNamingTheReasonAndTheCount: carrying outcomes between a background run and a later judgement is deleted with the store

// --- internal/tdd/mutants_carry_test.go
// ratchet: test_removed TestMutationReceipt_NotRequiredWhenTheLaneChangesNoSourceOrTestFile: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_NotRequiredWhenTheLaneChangesOnlyTestFiles: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_NotRequiredWhenTheLaneChangesOnlyManifestFiles: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_NotRequiredWhenTheLaneMixesTestManifestAndIgnorePaths: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_TestOnlyDiffNeverReachesTheVacuousCheck: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_StillRequiredWhenTheDiffCarriesOneSourceFile: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_CarriesForwardOverADocsOnlyCommit: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_RefusesToCarryForwardWhenOneSourceBlobDiffers: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_RefusesToCarryForwardFromAnotherBase: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_MissingReceiptRejectsWithOneRemedyLine: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_MissingReceiptNeverNamesAScriptTheRepoLacks: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_MissingReceiptNamesTheScriptWhenTheRepoHasOne: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutantsRunnerCommand_FallsThroughAnEmptyCargoDeclaredName: carrying outcomes between a background run and a later judgement is deleted with the store
// ratchet: test_removed TestMutationReceipt_MissingReceiptNamesTheDeclaredRunner: carrying outcomes between a background run and a later judgement is deleted with the store

// --- internal/tdd/mutants_ci_move_test.go
// ratchet: test_removed TestRunGoMutantsCI_APureMoveBetweenFilesMeasuresZeroAndRecordsMovedLines: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>`

// --- internal/tdd/mutants_ci_pipeline_test.go
// ratchet: test_removed TestNightlyMutants_SavesTheMutationOutcomeStoreEvenWhenTheMutantsStepFails: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>`

// --- internal/tdd/mutants_ci_producer_version_test.go
// ratchet: test_removed TestRunGoMutantsCI_DoesNotCountAMutantTwiceWhenTheProducerVersionChanged: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>`

// --- internal/tdd/mutants_ci_runlock_test.go
// ratchet: test_removed TestRunGoMutantsCI_WaitsForTheBoxWideMutationRunLockBeforeRunningGremlins: the CI half of `gate mutants go` is deleted; MeasureLane holds the same box-wide lock, proved by TestMeasureLane_HoldsTheMutantsRunLockForTheWholeToolInvocation
// ratchet: test_removed TestRunGoMutantsCI_OneJobPerContainerSkipsTheBoxWideLock: the CI half of `gate mutants go` is deleted; MeasureLane holds the same box-wide lock, proved by TestMeasureLane_HoldsTheMutantsRunLockForTheWholeToolInvocation

// --- internal/tdd/mutants_ci_test.go
// ratchet: test_removed TestRunGoMutantsCI_FailsOnASurvivorNobodyAccepted: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_PassesWhenEverySurvivorCarriesAReason: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_WritesASignedReceiptWhereTheWorkflowUploadsIt: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_FailsWhenTheScopeMatchedNoMutants: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_PassesWhenTheDiffCarriesNoMutableGo: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_StillFailsWhenProductionGoChangedAndNothingWasMeasured: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_FailsOnATimedOutMutant: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_FailsWhenTheToolWroteNoReport: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_RefusesToRunWithoutAMergeBase: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_CapsTheRunAtTheWorkersItWasGiven: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunCommandIn_ReportsTheChildsExitCode: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_MeasuresFromTheRepoRootNotTheDirectoryItWasHanded: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestNightlyMutants_RunsOnScheduleNotPerPush: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestAphrolloToml_RequiresTheProofAndMeasuresItLocally: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_ScopesTheRunToTheGivenMergeBase: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_SecondRunOverAnUnchangedTreeMeasuresZeroAndCarriesEverything: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestRunGoMutantsCI_AChangedFileReRunsItsMutantsCarryingTheUnchangedOne: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestCiMutantStorePath_EmptyStoreFallsBackToTheMachineLocalDefault: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses
// ratchet: test_removed TestCiMutantStorePath_ExplicitStoreWinsOverTheMachineLocalDefault: the CI half of `gate mutants go` is deleted; nightly CI calls `run --base <sha>` through the same code path a lane uses

// --- internal/tdd/mutants_clone_objects_test.go
// ratchet: test_removed TestGoMutantsTree_ChecksOutATipOnlyTheLaneWorktreeHasCommitted: the mutation worktree is deleted: cargo-mutants mutates the lane's tree in place

// --- internal/tdd/mutants_death_tail_test.go
// ratchet: test_removed TestRecordMutantsDeath_TailsTheStdoutLogWhenStderrIsEmpty: a death record described a detached run that ended without a receipt; a foreground run reports its own exit, proved by TestMeasure_NoVerdictExitRefusesWithStatusAndLog
// ratchet: test_removed TestRecordMutantsDeath_PrefersStderrWhenItHasContent: a death record described a detached run that ended without a receipt; a foreground run reports its own exit, proved by TestMeasure_NoVerdictExitRefusesWithStatusAndLog

// --- internal/tdd/mutants_fence_test.go
// ratchet: test_removed TestFence_ChangesWhenADependencyCrateChanges: the package fence existed to decide whether a stored outcome was still true; there is no store
// ratchet: test_removed TestFence_ChangesWhenASiblingSourceFileInTheSamePackageChanges: the package fence existed to decide whether a stored outcome was still true; there is no store
// ratchet: test_removed TestFence_ChangesWhenThePackagesTestSetChanges: the package fence existed to decide whether a stored outcome was still true; there is no store
// ratchet: test_removed TestFence_FollowsTheDependencyGraphTransitively: the package fence existed to decide whether a stored outcome was still true; there is no store
// ratchet: test_removed TestFence_TerminatesOnACycle: the package fence existed to decide whether a stored outcome was still true; there is no store
// ratchet: test_removed TestCargoWorkspaceDeps_ReadsPathDependenciesFromMetadata: the package fence existed to decide whether a stored outcome was still true; there is no store
// ratchet: test_removed TestScopeCarry_IsLimitedToTheLanesOwnFiles: the package fence existed to decide whether a stored outcome was still true; there is no store
// ratchet: test_removed TestPlanMutants_CarriesAnOutcomeAcrossARename: the package fence existed to decide whether a stored outcome was still true; there is no store
// ratchet: test_removed TestPlanMutants_DoesNotCarryAcrossAMoveIntoAnotherPackage: the package fence existed to decide whether a stored outcome was still true; there is no store

// --- internal/tdd/mutants_fullycarried_test.go
// ratchet: test_removed TestRunMutantsJob_WritesTheCarriedReceiptWithoutStartingAProducer: carrying every outcome forward is deleted with the store
// ratchet: test_removed TestRunMutantsJob_PrintsTheMutantsWorktreesOwnHeadEveryRun: carrying every outcome forward is deleted with the store

// --- internal/tdd/mutants_go_isolate_test.go
// ratchet: test_removed TestGoMutantsTree_ClonesOutEvenWithTheHooksGitDirInherited: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestGoMutantsTree_LeavesTheRunTreeWithNoRemoteToPushTo: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestMutantsChildEnv_CarriesNoGitVariableIntoTheRun: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestGoMutantsTree_ClonesOutOfALinkedWorktree: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestGoMutantsTree_ARewriteInTheRunTreeLeavesTheRealRepositoryAlone: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestGoMutantsTree_KeepsAStandaloneCheckoutAsItsOwnRunTree: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestRunGoMutantsJob_RunsGremlinsOutsideTheLinkedWorktree: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestRunGoMutantsJob_PrintsTheMutantsWorktreesOwnHeadEveryRun: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestRunGoMutantsJob_WaitsForTheBoxWideMutationRunLockBeforeRunningGremlins: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place
// ratchet: test_removed TestRunGoMutantsJob_WaitsForTheBoxWideLockBeforeCloningTheIsolatedTree: the isolated clone is deleted with the detached job: the run measures the lane's own tree in place

// --- internal/tdd/mutants_here_test.go
// ratchet: test_removed TestRunMutantsHere_BuildsTheCurrentLanesJobInsteadOfExitingSilently: buildMutantsJob is deleted; `gate mutants run` calls MeasureLane on the checkout it is typed in
// ratchet: test_removed TestRunMutantsHere_MeasuresFromTheSameLaneBaseTheMergeGateExpects: buildMutantsJob is deleted; `gate mutants run` calls MeasureLane on the checkout it is typed in
// ratchet: test_removed TestRunMutantsHere_RefusesASecondRunWhileThisRepoIsAlreadyMeasuring: buildMutantsJob is deleted; `gate mutants run` calls MeasureLane on the checkout it is typed in
// ratchet: test_removed TestRunMutantsHere_RunsWhenTheGoingRunIsMeasuringADifferentTree: buildMutantsJob is deleted; `gate mutants run` calls MeasureLane on the checkout it is typed in
// ratchet: test_removed TestRunMutantsHere_OnTrunkNamesTheBranchRatherThanSayingNothing: buildMutantsJob is deleted; `gate mutants run` calls MeasureLane on the checkout it is typed in
// ratchet: test_removed TestRunMutantsHere_WithoutOptInNamesTheSettingThatEnablesIt: buildMutantsJob is deleted; `gate mutants run` calls MeasureLane on the checkout it is typed in
// ratchet: test_removed TestRunMutantsJob_UnreadableJobPathIsAnErrorNotAQuietZero: buildMutantsJob is deleted; `gate mutants run` calls MeasureLane on the checkout it is typed in
// ratchet: test_removed TestRunMutantsHere_ADifferentProcessAtTheSameWorktreeNameIsNeverTreatedAsSelf: buildMutantsJob is deleted; `gate mutants run` calls MeasureLane on the checkout it is typed in

// --- internal/tdd/mutants_invocation_version_test.go
// ratchet: test_removed TestMutantsInvocation_Version_DiffersOnEachVerdictAffectingField: the invocation version existed to invalidate a stored outcome; there is no store
// ratchet: test_removed TestMutantsInvocationVersion_SameAcrossDifferentDiffsAndPackageLists: the invocation version existed to invalidate a stored outcome; there is no store
// ratchet: test_removed TestMutantsInvocationVersion_DiffersWhenTheTestToolDiffers: the invocation version existed to invalidate a stored outcome; there is no store
// ratchet: test_removed TestMutantsInvocationVersion_EmptyWhenTheProducerVersionIsEmpty: the invocation version existed to invalidate a stored outcome; there is no store
// ratchet: test_removed TestStampInvocationVersion_LeavesTheInputSliceUntouched: the invocation version existed to invalidate a stored outcome; there is no store
// ratchet: test_removed TestPlanMutants_DoesNotCarryAMutantMeasuredUnderADifferentInvocationVersion: the invocation version existed to invalidate a stored outcome; there is no store
// ratchet: test_removed TestPlanMutants_CarriesOnePackageWhileReRunningAnotherUnderADifferentInvocation: the invocation version existed to invalidate a stored outcome; there is no store

// --- internal/tdd/mutants_job_test.go
// ratchet: test_removed TestStartMutantsJob_NeverCancelsTheRunItSupersedes: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStartMutantsJob_ASecondCommitGetsADifferentWorktreeWhileTheFirstJobStillRuns: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStartMutantsJob_OnlyForAnOptedInLane: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStartMutantsJob_OptsInThroughAphrolloToml: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStartMutantsJob_SkipsTheLocalRunWhenTheRepoRunsMutationInCI: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStartMutantsJob_TheCargoWorkspaceDeclaresTheSameOptOut: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestMutantsWorktree_IsOneDedicatedTreeUnderTheRepoMutantsRoot: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestMutantsWorktree_RootResolvesFromThePrimaryCheckoutNotTheLaneRoot: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStartMutantsJob_WorktreePrepareFailureIsLoggedAndReturned: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestMutantsProducerFlags_MutatesInPlaceOverTheLaneDiff: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestMutantsProducerFlags_SkipsTheBaselineOnlyWhenItWasAlreadyProven: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestMutantsProducerFlags_ScopesTheBaselineToTouchedPackagesOnly: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestMutantsProducerFlags_OmitsPackageFlagsWhenTouchedSetIsUndeterminable: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestTipSuiteGreen_ReadsTheLastGateRunForThisCheckout: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestMutationReceipt_MissingReceiptNamesTheRunningJob: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestRunningMutantsJobs_ForgetsAJobWhoseProcessIsGone: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestPidRunning_SeesThisProcessAndNotPidZero: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStatusLine_SaysMutantsWhileAJobRunsForThisProject: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStatusLine_AMutantsJobInAnotherProjectLeavesThisBadgeQuiet: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestStatusLine_AMutantsJobInASiblingLaneLeavesThisBadgeQuiet: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestPostCommitHook_WritesTheNoteAndStartsTheRun: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground
// ratchet: test_removed TestSaveMutantsJob_AConcurrentSaveWaitsForOneAlreadyInFlightRatherThanLosingAnEntry: the detached job, its file, its registry and its post-commit spawn are deleted; the run is in the foreground

// --- internal/tdd/mutants_lane_reclaim_test.go
// ratchet: test_removed TestReclaimStaleMutantsLanes_RemovesTheTreeOfALaneThatIsGone: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestReclaimStaleMutantsLanes_KeepsTheTreeOfALaneThatIsStillThere: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestReclaimStaleMutantsLanes_LeavesADirectoryItCannotAccountFor: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestReclaimStaleMutantsLanes_RemovesALegacyPerLaneTargetDir: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestReclaimStaleMutantsLanes_KeepsALegacyTargetDirWhileARunIsAlive: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestReclaimStaleMutantsLanes_RemovesAnAlternateWhoseJobIsOverEvenThoughItsLaneStillExists: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestReclaimStaleMutantsLanes_KeepsAnAlternateAJobStillHolds: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestReclaimStaleMutantsLanes_AlsoRemovesTheRunCloneBesideTheTree: the per-lane mutation worktree is deleted with the detached job

// --- internal/tdd/mutants_liveness_test.go
// ratchet: test_removed TestRunningMutantsJobs_ARecycledPidIsNotTheJob: the job registry's liveness probe is deleted with the job registry
// ratchet: test_removed TestRunningMutantsJobs_AJobWithNoRecordedStartIsJudgedByPidAlone: the job registry's liveness probe is deleted with the job registry
// ratchet: test_removed TestProcessStartToken_IsStableAndSpecificToTheProcess: the job registry's liveness probe is deleted with the job registry

// --- internal/tdd/mutants_missingreceipt_test.go
// ratchet: test_removed TestMutationReceipt_MissingReceiptNamesTheJobForThisTreeNotTheNewestOne: there is no receipt for a merge to find missing
// ratchet: test_removed TestMutationReceipt_MissingReceiptReportsQueuedBehindADifferentHolder: there is no receipt for a merge to find missing

// --- internal/tdd/mutants_movediff_test.go
// ratchet: test_removed TestLaneDiff_APureMoveBetweenFilesLeavesNoLinesToMutate: the move-aware diff filtered a lane's own diff for the producer; measureDiff hands cargo-mutants the plain base-to-worktree diff
// ratchet: test_removed TestLaneDiff_AMoveWithOneEditedLineKeepsOnlyThatLine: the move-aware diff filtered a lane's own diff for the producer; measureDiff hands cargo-mutants the plain base-to-worktree diff
// ratchet: test_removed TestRunMutantsJob_ALaneOfPureMovesGetsAZeroMutantReceipt: the move-aware diff filtered a lane's own diff for the producer; measureDiff hands cargo-mutants the plain base-to-worktree diff
// ratchet: test_removed TestMovedOnlyFiles_APureMoveExcludesBothFilesAndCountsTheLines: the move-aware diff filtered a lane's own diff for the producer; measureDiff hands cargo-mutants the plain base-to-worktree diff
// ratchet: test_removed TestMovedOnlyFiles_AMoveWithOneEditedLineKeepsThatFileInTheRun: the move-aware diff filtered a lane's own diff for the producer; measureDiff hands cargo-mutants the plain base-to-worktree diff

// --- internal/tdd/mutants_names_test.go
// ratchet: test_removed TestMutantNames_AreTheToolsOwnLineVerbatim: the receipt's own MutantName decoding is deleted with the receipt; a mutant is named by outcomeName, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt
// ratchet: test_removed TestParseMutantLine_KeepsTheColumnThatTellsTwoMutantsApart: the receipt's own MutantName decoding is deleted with the receipt; a mutant is named by outcomeName, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt
// ratchet: test_removed TestMutantStore_KeepsBothMutantsOnOneLineApart: the receipt's own MutantName decoding is deleted with the receipt; a mutant is named by outcomeName, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt
// ratchet: test_removed TestMutantsProducerFlags_ExcludesTheVerbatimLineAnchored: the receipt's own MutantName decoding is deleted with the receipt; a mutant is named by outcomeName, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt
// ratchet: test_removed TestMutantsProducerFlags_StaysInsideTheEnvironmentBlockLimit: the receipt's own MutantName decoding is deleted with the receipt; a mutant is named by outcomeName, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt
// ratchet: test_removed TestReadMutantsOut_ReadsRealVerdictFilesWithTheirColumns: the receipt's own MutantName decoding is deleted with the receipt; a mutant is named by outcomeName, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt

// --- internal/tdd/mutants_nothingtomutate_test.go
// ratchet: test_removed TestRunMutantsProducer_WritesAnHonestReceiptWhenTheToolFindsNothingToMutate: the producer's no-source markers are deleted; a diff naming no mutable source is answered by measureSkipped
// ratchet: test_removed TestRunMutantsProducer_StampsAnExistingReceiptWhenTheToolFiltersOutEveryMutant: the producer's no-source markers are deleted; a diff naming no mutable source is answered by measureSkipped
// ratchet: test_removed TestRunMutantsProducer_StillFailsWhenExitIsNonZeroWithNoNothingToMutateMarker: the producer's no-source markers are deleted; a diff naming no mutable source is answered by measureSkipped

// --- internal/tdd/mutants_plan_test.go
// ratchet: test_removed TestPlanMutants_CarriesAnUnchangedFilesOutcomes: the incremental plan existed to decide what a stored outcome still answered for; there is no store
// ratchet: test_removed TestPlanMutants_RerunsAPackageWhoseTestSetHashChanged: the incremental plan existed to decide what a stored outcome still answered for; there is no store
// ratchet: test_removed TestPlanMutants_RunsAMutantWhoseFileBlobChanged: the incremental plan existed to decide what a stored outcome still answered for; there is no store
// ratchet: test_removed TestPlanMutants_RunsAMutantThePreviousRunNeverMeasured: the incremental plan existed to decide what a stored outcome still answered for; there is no store
// ratchet: test_removed TestPlanMutants_RunsEverythingWhenThePreviousReceiptRecordsNoBlobs: the incremental plan existed to decide what a stored outcome still answered for; there is no store
// ratchet: test_removed TestPlanMutants_RunsEverythingWithNoPreviousReceipt: the incremental plan existed to decide what a stored outcome still answered for; there is no store
// ratchet: test_removed TestPlanMutants_StampsTheMeasurementItJudgedAgainst: the incremental plan existed to decide what a stored outcome still answered for; there is no store
// ratchet: test_removed TestPlanMutants_DoesNotCarryAMutantMeasuredUnderADifferentProducerVersion: the incremental plan existed to decide what a stored outcome still answered for; there is no store
// ratchet: test_removed TestDedupByMutantKey_DropsAnEntryFromExtraAlreadyPresentInPrimary: the incremental plan existed to decide what a stored outcome still answered for; there is no store

// --- internal/tdd/mutants_producer_version_test.go
// ratchet: test_removed TestMutantsProducerVersionCmd_PicksTheToolTheWorktreeActuallyHas: the producer version stamped stored outcomes; there is no store and no repo-owned producer
// ratchet: test_removed TestMutantsProducerVersion_IsEmptyWhenTheProbeErrors: the producer version stamped stored outcomes; there is no store and no repo-owned producer
// ratchet: test_removed TestMutantsProducerVersion_ReturnsTheProbesTrimmedOutput: the producer version stamped stored outcomes; there is no store and no repo-owned producer
// ratchet: test_removed TestStampProducerVersion_LeavesTheInputSliceUntouched: the producer version stamped stored outcomes; there is no store and no repo-owned producer

// --- internal/tdd/mutants_recount_test.go
// ratchet: test_removed TestAdoptCarriedOutcomes_KeepsAShellProducersCountsWhenOutcomesIsAbsent: recounting a carried receipt is deleted with the receipt
// ratchet: test_removed TestAdoptCarriedOutcomes_SignsAShellProducersReceiptCorrectly: recounting a carried receipt is deleted with the receipt

// --- internal/tdd/mutants_replaced_test.go
// ratchet: test_removed TestJobsRunningReplacedBinary_FindsAJobStillOnTheStalePath: the job registry is deleted; ReplacedBinaryJobsLine reads the run lock's own owner record instead, proved by TestReplacedBinaryJobsLine_NamesARunStillExecutingTheReplacedBinary
// ratchet: test_removed TestJobsRunningReplacedBinary_IgnoresAJobOnADifferentPath: the job registry is deleted; ReplacedBinaryJobsLine reads the run lock's own owner record instead, proved by TestReplacedBinaryJobsLine_NamesARunStillExecutingTheReplacedBinary
// ratchet: test_removed TestJobsRunningReplacedBinary_CoversEveryRepoNotJustOne: the job registry is deleted; ReplacedBinaryJobsLine reads the run lock's own owner record instead, proved by TestReplacedBinaryJobsLine_NamesARunStillExecutingTheReplacedBinary
// ratchet: test_removed TestJobsRunningReplacedBinary_EmptyStalePathReportsNothing: the job registry is deleted; ReplacedBinaryJobsLine reads the run lock's own owner record instead, proved by TestReplacedBinaryJobsLine_NamesARunStillExecutingTheReplacedBinary
// ratchet: test_removed TestReplacedBinaryJobsLine_NamesEachJobsBranchAndPID: the job registry is deleted; ReplacedBinaryJobsLine reads the run lock's own owner record instead, proved by TestReplacedBinaryJobsLine_NamesARunStillExecutingTheReplacedBinary
// ratchet: test_removed TestReplacedBinaryJobsLine_EmptyWhenNothingIsRunningTheReplacedBinary: the job registry is deleted; ReplacedBinaryJobsLine reads the run lock's own owner record instead, proved by TestReplacedBinaryJobsLine_NamesARunStillExecutingTheReplacedBinary

// --- internal/tdd/mutants_resume_test.go
// ratchet: test_removed TestReadMutantsOut_KeepsEveryVerdictAnInterruptedRunReached: resuming an interrupted detached run is deleted with the detached run
// ratchet: test_removed TestResumeMutants_MeasuresOnlyTheMutantsWithoutAVerdict: resuming an interrupted detached run is deleted with the detached run
// ratchet: test_removed TestMutantsProducerFlags_ExcludesTheMutantsAlreadyJudged: resuming an interrupted detached run is deleted with the detached run
// ratchet: test_removed TestMutantsJobLogs_LiveInGateStateNeverInAWorktree: resuming an interrupted detached run is deleted with the detached run
// ratchet: test_removed TestMutationReceipt_MissingReceiptNamesARunThatDied: resuming an interrupted detached run is deleted with the detached run
// ratchet: test_removed TestMutantsDeath_ClearedWhenTheSameTreeFinishes: resuming an interrupted detached run is deleted with the detached run

// --- internal/tdd/mutants_root_unresolvable_test.go
// ratchet: test_removed TestMutantsRootDir_RefusesRatherThanNestUnderTheLaneWhenPrimaryUnresolvable: the per-lane mutation worktree is deleted with the detached job

// --- internal/tdd/mutants_run_blob_test.go
// ratchet: test_removed TestRunMutantsJob_DoesNotCarryAnOutcomeMeasuredAtAnotherBlob: the blob check decided whether a stored outcome was still true; there is no store

// --- internal/tdd/mutants_run_rewritten_test.go
// ratchet: test_removed TestRunMutantsJob_LogsTipRewrittenInsteadOfWorktreeFailedWhenTheLaneMovesOn: a rewritten tip mattered to a detached run started before it; a foreground run measures the tree in front of it
// ratchet: test_removed TestRunMutantsJob_TreatsAGitFailureAsWorktreeFailedNotARewrittenTip: a rewritten tip mattered to a detached run started before it; a foreground run measures the tree in front of it

// --- internal/tdd/mutants_status_test.go
// ratchet: test_removed TestComputeMutantsStatus_NoneWhenNothingHasEverMeasuredThisTree: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestComputeMutantsStatus_ErrorsOutsideAGitRepository: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestComputeMutantsStatus_GoingReportsMeasuringWhenNoOtherProcessHoldsTheLock: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestComputeMutantsStatus_GoingReportsWaitingOnLockWhenAnotherPidHoldsIt: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestComputeMutantsStatus_DiedReportsExitCodeAndLogPath: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestComputeMutantsStatus_DoneReadsVerdictAndCounts: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestComputeMutantsStatus_DoneTakesPrecedenceOverARunningJobRecord: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestFormatMutantsStatus_ExitPassWhenReceiptVerdictIsCleanPass: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestFormatMutantsStatus_ReportsWouldNotMergeForAVacuousReceiptWithNoZeroReason: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestFormatMutantsStatus_AZeroReasonMakesAnOtherwiseVacuousReceiptMergeable: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestFormatMutantsStatus_ExitFailWhenReceiptHasUnacceptedSurvivors: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestFormatMutantsStatus_ExitNoneWhenNoRunStarted: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestFormatMutantsStatus_ExitGoingWhenAJobIsAlive: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestFormatMutantsStatus_ExitDiedWhenTheRunEndedWithoutAReceipt: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestWaitMutantsStatus_ReturnsImmediatelyWithoutWaitingWhenAlreadyTerminal: `gate mutants status` is deleted with the detached run it reported on
// ratchet: test_removed TestWaitMutantsStatus_BlocksOnTheJobThenRereadsTheTerminalReceipt: `gate mutants status` is deleted with the detached run it reported on

// --- internal/tdd/mutants_store_test.go
// ratchet: test_removed TestMutantStore_CarriesAcrossLanesForTheSameBlobAndTestSet: the outcome store is deleted: measurement and judgement are one event, so there is nothing for a cache to carry between them
// ratchet: test_removed TestMutantStore_AChangedTestSetInvalidatesOnlyItsOwnPackage: the outcome store is deleted: measurement and judgement are one event, so there is nothing for a cache to carry between them
// ratchet: test_removed TestPruneMutantStore_DropsEntriesWhoseBlobIsGoneOrThatAreOld: the outcome store is deleted: measurement and judgement are one event, so there is nothing for a cache to carry between them
// ratchet: test_removed TestMutantStore_IsSchemaStampedAndWrittenWhole: the outcome store is deleted: measurement and judgement are one event, so there is nothing for a cache to carry between them
// ratchet: test_removed TestLoadMutantStore_DiscardsAStoreWrittenUnderAnOlderVerdictSchema: the outcome store is deleted: measurement and judgement are one event, so there is nothing for a cache to carry between them
// ratchet: test_removed TestMergeMutantStore_KeepsTheNewestVerdictPerMutant: the outcome store is deleted: measurement and judgement are one event, so there is nothing for a cache to carry between them
// ratchet: test_removed TestMergeMutantStore_AConcurrentMergeWaitsForOneAlreadyInFlight: the outcome store is deleted: measurement and judgement are one event, so there is nothing for a cache to carry between them

// --- internal/tdd/mutants_target_shared_test.go
// ratchet: test_removed TestMutantsTargetDir_IsOnePerRepoSharedByEveryLane: the shared mutation build directory belonged to the detached job's own worktree

// --- internal/tdd/mutants_trailer_test.go
// ratchet: test_removed TestMutantsRunRequested_TrueForATrailerCaseInsensitiveOnKeyAndValue: the `Mutants: run` commit trailer asked the post-commit hook for a run; there is no post-commit run to ask for
// ratchet: test_removed TestMutantsRunRequested_FalseWithNoTrailer: the `Mutants: run` commit trailer asked the post-commit hook for a run; there is no post-commit run to ask for
// ratchet: test_removed TestMutantsRunRequested_ReportsAGitFailureRatherThanReportingNoTrailer: the `Mutants: run` commit trailer asked the post-commit hook for a run; there is no post-commit run to ask for
// ratchet: test_removed TestMutantsRunRequested_FalseWhenTheWordsAppearOnlyInBodyProse: the `Mutants: run` commit trailer asked the post-commit hook for a run; there is no post-commit run to ask for
// ratchet: test_removed TestStartMutantsJob_RefusesAnOptedInLaneCommitWithNoMutantsRunTrailer: the `Mutants: run` commit trailer asked the post-commit hook for a run; there is no post-commit run to ask for
// ratchet: test_removed TestRunMutantsHere_IgnoresTheTrailerRequirement: the `Mutants: run` commit trailer asked the post-commit hook for a run; there is no post-commit run to ask for

// --- internal/tdd/mutants_treestate_ron_test.go
// ratchet: test_removed TestPlanDiffFiles_AgreesWithLaneHasNothingToMutateOnAnUnownedRonFile: the tree state existed to invalidate stored outcomes; there is no store

// --- internal/tdd/mutants_treestate_test.go
// ratchet: test_removed TestTreeStateFromListing_ReadsABlobAndAPackageForEveryFile: the tree state existed to invalidate stored outcomes; there is no store
// ratchet: test_removed TestFence_MovesForASourceEditAndForATestEdit: the tree state existed to invalidate stored outcomes; there is no store
// ratchet: test_removed TestPlanDiffFiles_LeavesOutAFileNothingChangedAround: the tree state existed to invalidate stored outcomes; there is no store
// ratchet: test_removed TestPlanDiffFiles_PullsInAWholePackageWhoseTestSetChanged: the tree state existed to invalidate stored outcomes; there is no store
// ratchet: test_removed TestPlanDiffFiles_AProducerVersionMismatchPutsAnOtherwiseUnchangedFileBackInTheRun: the tree state existed to invalidate stored outcomes; there is no store
// ratchet: test_removed TestPlanDiffFiles_TakesEveryMutableFileOnAFirstRun: the tree state existed to invalidate stored outcomes; there is no store

// --- internal/tdd/mutants_watch_test.go
// ratchet: test_removed TestTailNewLines_HoldsBackAPartialLineUntilItIsTerminated: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestParseMutantsFoundLine_ReadsTheCountFromCargoMutantsOwnLine: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestIsUnmutatedBaselineLine_MatchesCargoMutantsOwnLine: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestMutantsWatchStep_ReportsNewMutantsOnceAndFlagsSurvivors: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestWatchMutantsHere_ReportsDoneWhenAReceiptAlreadyExists: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestWatchMutantsHere_ReportsDiedWhenTheRunEndedWithNoReceipt: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestWatchMutantsHere_ReportsNoneWhenNothingHasEverBeenStarted: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestWatchMutantsHere_ReportsKilledWhenTheProcessIsGoneWithNoDeathRecord: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestWatchMutantsHere_DoesNotReportKilledWhileTheProcessIsStillAlive: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestWatchMutantsHere_ReportsStalledWhenALiveProcessNeverAdvances: `gate mutants watch` is deleted with the detached run it subscribed to
// ratchet: test_removed TestWatchMutantsHere_JobFlagNeedsNoGitRepository: `gate mutants watch` is deleted with the detached run it subscribed to

// --- internal/tdd/mutants_worktree_perlane_test.go
// ratchet: test_removed TestMutantsWorktreeDir_GivesTwoLanesOfOneRepoTwoDirectories: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestMutantsWorktreeDir_KeepsEveryLanesTreeUnderTheRepoMutantsRoot: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestMutantsWorktreeDir_AnswersTheSameDirectoryForOneLaneTwice: the per-lane mutation worktree is deleted with the detached job

// --- internal/tdd/mutants_worktree_reservation_test.go
// ratchet: test_removed TestChooseMutantsWorktree_TwoConcurrentCallsForTheSameBaseNeverBothChooseIt: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestChooseMutantsWorktree_TwoConcurrentCallsAtTheSameTipNeverChooseTheSameAlternate: the per-lane mutation worktree is deleted with the detached job
// ratchet: test_removed TestSaveMutantsJob_NeverDropsAnotherProcessesLiveClaimAtTheSameWorktreeName: the per-lane mutation worktree is deleted with the detached job

// --- internal/tdd/receipt_catchup_test.go
// ratchet: test_removed TestMutationReceiptStage_ACatchUpMergeOfMainIntoALaneNeedsNoReceipt: the receipt stage is deleted, and with it the catch-up waiver it needed
// ratchet: test_removed TestMutationReceiptStage_ALaneMergingIntoMainStillNeedsAReceipt: the receipt stage is deleted, and with it the catch-up waiver it needed
// ratchet: test_removed TestMutationReceiptStage_OptsInThroughAphrolloToml: the receipt stage is deleted, and with it the catch-up waiver it needed
// ratchet: test_removed TestMutationReceiptStage_StandsDownWhenTheProofIsMeasuredInCI: the receipt stage is deleted, and with it the catch-up waiver it needed
// ratchet: test_removed TestMutationReceiptStage_ABranchMainAlreadyContainsIsACatchUp: the receipt stage is deleted, and with it the catch-up waiver it needed

// --- internal/tdd/receipt_coherence_test.go
// ratchet: test_removed TestCheckMutationReceipt_RefusesAcceptedExceedingMutantsTotal: the receipt's count-coherence checks are deleted with the receipt: the counts are this run's own now
// ratchet: test_removed TestCheckMutationReceipt_RefusesCategorySumExceedingMutantsTotal: the receipt's count-coherence checks are deleted with the receipt: the counts are this run's own now
// ratchet: test_removed TestCheckMutationReceipt_RefusesMoreUnacceptedThanSurvivors: the receipt's count-coherence checks are deleted with the receipt: the counts are this run's own now
// ratchet: test_removed TestCheckMutationReceipt_RefusesAnUnacceptedEntryNotInSurvivors: the receipt's count-coherence checks are deleted with the receipt: the counts are this run's own now
// ratchet: test_removed TestCheckMutationReceipt_ToleratesAnOlderProducerThatOmitsMutantsTotal: the receipt's count-coherence checks are deleted with the receipt: the counts are this run's own now

// --- internal/tdd/receipt_judgelocal_test.go
// ratchet: test_removed TestMutationReceiptStage_StillDemandsAReceiptWhenOnlyTheRunIsRemote: mutants-judge-local is retired; the measurement happens where the merge does
// ratchet: test_removed TestMutationJudgedLocally_DefaultsToWhereTheRunHappens: mutants-judge-local is retired; the measurement happens where the merge does
// ratchet: test_removed TestMutationJudgedLocally_CanStandDownWhileTheRunStaysLocal: mutants-judge-local is retired; the measurement happens where the merge does

// --- internal/tdd/receipt_repoid_test.go
// ratchet: test_removed TestCheckMutationReceipt_AcceptsAReceiptFromAnotherCheckoutOfTheSameRepo: the repository-identity check existed so one machine could trust a document another wrote
// ratchet: test_removed TestCheckMutationReceipt_RefusesAReceiptFromADifferentRepository: the repository-identity check existed so one machine could trust a document another wrote
// ratchet: test_removed TestCheckMutationReceipt_FallsBackToThePathWhenAReceiptCarriesNoIdentity: the repository-identity check existed so one machine could trust a document another wrote

// --- internal/tdd/receipt_schema_test.go
// ratchet: test_removed TestSignReceipt_StampsCurrentSchema: the receipt schema is deleted with the receipt
// ratchet: test_removed TestSignReceipt_OverwritesACallerSuppliedSchema: the receipt schema is deleted with the receipt
// ratchet: test_removed TestCheckMutationReceipt_RefusesANewerSchemaProducer: the receipt schema is deleted with the receipt
// ratchet: test_removed TestOlderProducerZeroReasonMessage_NamesTheSchemaGap: the receipt schema is deleted with the receipt
// ratchet: test_removed TestCheckMutationReceipt_SchemaZeroStaysAmbiguous: the receipt schema is deleted with the receipt

// --- internal/tdd/receipt_sign_test.go
// ratchet: test_removed TestReceiptSigning_VerifiesWhatTheCommandWroteAndRejectsAnEditedByte: the receipt MAC is deleted with the receipt: a measurement the merge performs itself needs no signature
// ratchet: test_removed TestReceiptSigning_LogsAForgedReceipt: the receipt MAC is deleted with the receipt: a measurement the merge performs itself needs no signature
// ratchet: test_removed TestReceiptSigning_RefusesAnUnsignedReceiptEvenWithAMatchingTipAndBase: the receipt MAC is deleted with the receipt: a measurement the merge performs itself needs no signature
// ratchet: test_removed TestReceiptSigning_BlocksWhenTheSigningKeyCannotBeRead: the receipt MAC is deleted with the receipt: a measurement the merge performs itself needs no signature
// ratchet: test_removed TestReceiptSigning_ReportsTruncatedKeyAsUnverifiableNotForgedAndNeverOverwritesIt: the receipt MAC is deleted with the receipt: a measurement the merge performs itself needs no signature
// ratchet: test_removed TestReceiptKey_IsCreatedOncePrivateToThisUser: the receipt MAC is deleted with the receipt: a measurement the merge performs itself needs no signature
// ratchet: test_removed TestReceiptSigning_CoversTheBodyRatherThanTheFormatting: the receipt MAC is deleted with the receipt: a measurement the merge performs itself needs no signature
// ratchet: test_removed TestSignReceiptFile_OutcomesSHAIsTheHashOfTheOutcomesFileBytes: the receipt MAC is deleted with the receipt: a measurement the merge performs itself needs no signature

// --- internal/tdd/receipt_test.go
// ratchet: test_removed TestMutationReceipt_RefusesAMergeWithoutProof: the receipt is deleted; the merge measures the tree in front of it instead of reading a document about it
// ratchet: test_removed TestMutationReceipt_GoOnlyRepoIsNeverToldToRunAScriptItDoesNotHave: the receipt is deleted; the merge measures the tree in front of it instead of reading a document about it
// ratchet: test_removed TestMutationReceipt_IsFoundByTheLaneTipTreeAlone: the receipt is deleted; the merge measures the tree in front of it instead of reading a document about it
// ratchet: test_removed TestMutationReceipt_AcceptsAProvenTree: the receipt is deleted; the merge measures the tree in front of it instead of reading a document about it
// ratchet: test_removed TestMutationReceipt_AcceptedReceiptIsLogged: the receipt is deleted; the merge measures the tree in front of it instead of reading a document about it
// ratchet: test_removed TestMutationReceiptPathLivesInTheGateStateDir: the receipt is deleted; the merge measures the tree in front of it instead of reading a document about it
// ratchet: test_removed TestMutationReceipt_OnlyWhenTheWorkspaceAsksForIt: the receipt is deleted; the merge measures the tree in front of it instead of reading a document about it
// ratchet: test_removed TestMechanical_RunsTheReceiptGateForAnOptedInWorkspace: the receipt is deleted; the merge measures the tree in front of it instead of reading a document about it

// --- internal/tdd/receipt_worktree_test.go
// ratchet: test_removed TestCheckMutationReceipt_MainCheckoutAccepts: the worktree-dirty check on a receipt is deleted; the run's own tree-changed guard covers the same ground, proved by TestMeasure_RunThatLeavesTheTreeChangedIsRefused
// ratchet: test_removed TestCheckMutationReceipt_LinkedWorktreeDifferentName_Accepts: the worktree-dirty check on a receipt is deleted; the run's own tree-changed guard covers the same ground, proved by TestMeasure_RunThatLeavesTheTreeChangedIsRefused
// ratchet: test_removed TestCheckMutationReceipt_DifferentRepoSameFolderName_Refuses: the worktree-dirty check on a receipt is deleted; the run's own tree-changed guard covers the same ground, proved by TestMeasure_RunThatLeavesTheTreeChangedIsRefused
// ratchet: test_removed TestMechanical_LinkedWorktreeDifferentName_AcceptsMainCheckoutReceipt: the worktree-dirty check on a receipt is deleted; the run's own tree-changed guard covers the same ground, proved by TestMeasure_RunThatLeavesTheTreeChangedIsRefused

// --- internal/tdd/receipt_zeroreason_test.go
// ratchet: test_removed TestMutationReceipt_MergesAZeroMutantReceiptThatNamesItsOwnZeroReason: the vacuous-receipt check is deleted; a diff naming no mutable source is answered by measureSkipped
// ratchet: test_removed TestMutationReceipt_StillRefusesAZeroMutantReceiptWithNoZeroReason: the vacuous-receipt check is deleted; a diff naming no mutable source is answered by measureSkipped

// --- internal/tdd/receiptbase_test.go
// ratchet: test_removed TestMutationReceipt_RefusesAReceiptTakenAgainstAnotherBase: the base-mismatch check existed because a document could describe a different base; the run takes its own base
// ratchet: test_removed TestMutationReceipt_AcceptsAReceiptPinnedToThisMergeBase: the base-mismatch check existed because a document could describe a different base; the run takes its own base
// ratchet: test_removed TestMutationReceipt_AcceptsButRecordsAnUnpinnedReceipt: the base-mismatch check existed because a document could describe a different base; the run takes its own base
// ratchet: test_removed TestMutationReceipt_DoesNotJudgeTheBaseItCannotName: the base-mismatch check existed because a document could describe a different base; the run takes its own base

// --- internal/tdd/receiptschema_test.go
// ratchet: test_removed TestMutationReceipt_DecodesRealProducerOutput: the receipt schema is deleted with the receipt
// ratchet: test_removed TestMutationReceipt_AcceptsRealProducerOutput: the receipt schema is deleted with the receipt
// ratchet: test_removed TestMutationReceipt_RefusesKnownWrongProducerOutputWhereAcceptedExceedsTotal: the receipt schema is deleted with the receipt
// ratchet: test_removed TestMutationReceipt_MatchesARepoNamedByItsGitDir: the receipt schema is deleted with the receipt
// ratchet: test_removed TestMutationReceipt_RefusesUnacceptedSurvivorsByName: the receipt schema is deleted with the receipt

// --- internal/tdd/receiptsurvivor_test.go
// ratchet: test_removed TestMutationReceipt_DecodesObjectSurvivorsAndMergesWhenAllAccepted: the receipt's survivor accounting is deleted; judgeMutants names every unaccepted survivor, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt
// ratchet: test_removed TestMutationReceipt_RefusesAnUnacceptedObjectSurvivorByFileAndLine: the receipt's survivor accounting is deleted; judgeMutants names every unaccepted survivor, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt
// ratchet: test_removed TestMutationReceipt_StillDecodesStringSurvivors: the receipt's survivor accounting is deleted; judgeMutants names every unaccepted survivor, proved by TestGateMutantsRun_RefusesOnUnacceptedMissedNamingIt

// --- internal/tdd/rejectonce_test.go
// ratchet: test_removed TestMechanical_ReceiptRejectionIsPrintedOnce: the receipt stage is deleted from Mechanical, so there is no rejection for it to print twice

// --- internal/tdd/repoidentity_test.go
// ratchet: test_removed TestRepoIdentity_IsTheSameInACloneAsInTheOriginal: repoIdentity existed so one machine could trust a document another wrote; there is no document
// ratchet: test_removed TestRepoIdentity_IgnoresRootsReachableOnlyFromNonHeadRefs: repoIdentity existed so one machine could trust a document another wrote; there is no document
// ratchet: test_removed TestRepoIdentity_DiffersBetweenTwoUnrelatedRepositories: repoIdentity existed so one machine could trust a document another wrote; there is no document
// ratchet: test_removed TestRepoIdentity_IsEmptyOutsideARepository: repoIdentity existed so one machine could trust a document another wrote; there is no document
