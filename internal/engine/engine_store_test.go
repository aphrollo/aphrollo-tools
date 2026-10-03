package engine

import (
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/kernel"
	"github.com/aphrollo/aphrollo-tools/internal/store"
)

// diskStore is the on-disk store over a directory of its own, with the repo's
// config as the engine decides with it, so a rebuild from its log folds the
// same way.
type diskStore struct{ *store.Store }

func newDiskStore(t testing.TB, cfg kernel.Config) testStore {
	t.Helper()
	s, err := store.Open(t.TempDir(), store.Options{Config: cfg})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return diskStore{s}
}

// TestHandle_runsOverTheOnDiskStoreToo runs every test of Handle that goes
// through a store again over the on-disk one, property tests and races
// included: the engine's contract is the Store interface's, and the in-memory
// store is only one reading of it. A test that reads the log (Events) reads the
// disk store's.
func TestHandle_runsOverTheOnDiskStoreToo(t *testing.T) {
	prev := newTestStore
	newTestStore = newDiskStore
	t.Cleanup(func() { newTestStore = prev })
	for name, fn := range map[string]func(*testing.T){
		"factSequencesMoveLaneAndUnit":                                TestHandle_factSequencesMoveLaneAndUnit,
		"returnsTheEffectsAndDoesNotRunThem":                          TestHandle_returnsTheEffectsAndDoesNotRunThem,
		"logsFactsInOrderAndNoQuestion":                               TestHandle_logsFactsInOrderAndNoQuestion,
		"questionMovesAndSavesNothing":                                TestHandle_questionMovesAndSavesNothing,
		"untestedCodeEditIsGuidedOnceUnderWarn":                       TestHandle_untestedCodeEditIsGuidedOnceUnderWarn,
		"untestedCodeEditDeniesEveryTimeUnderEnforce":                 TestHandle_untestedCodeEditDeniesEveryTimeUnderEnforce,
		"guidedFlagSurvivesLaterFacts":                                TestHandle_guidedFlagSurvivesLaterFacts,
		"noLaneFactIsRefusedAndQuestionIsStillDecided":                TestHandle_noLaneFactIsRefusedAndQuestionIsStillDecided,
		"cancelledContextTouchesNothing":                              TestHandle_cancelledContextTouchesNothing,
		"storeFailuresSurface":                                        TestHandle_storeFailuresSurface,
		"lostUpdateIsRetriedAndLoggedOnce":                            TestHandle_lostUpdateIsRetriedAndLoggedOnce,
		"retriesAreBounded":                                           TestHandle_retriesAreBounded,
		"contendedQuestionStillAnswers":                               TestHandle_contendedQuestionStillAnswers,
		"lanesAreSeparateRecords":                                     TestHandle_lanesAreSeparateRecords,
		"heldUnitIsGuidedOnceNotDenied":                               TestHandle_heldUnitIsGuidedOnceNotDenied,
		"oneAttemptIsOneAttemptNotTheDefault":                         TestHandle_oneAttemptIsOneAttemptNotTheDefault,
		"twoRacingFactsLoseNoUpdate":                                  TestHandle_twoRacingFactsLoseNoUpdate,
		"manyRacingFactsLoseNoUpdate":                                 TestHandle_manyRacingFactsLoseNoUpdate,
		"equalsFoldingKernelStepsDirectly":                            TestHandle_equalsFoldingKernelStepsDirectly,
		"questionsChangeNothingButTheGuidedFlags":                     TestHandle_questionsChangeNothingButTheGuidedFlags,
		"Unseen_isExactlyWhatNoReachingDeliveryCovered":               TestUnseen_isExactlyWhatNoReachingDeliveryCovered,
		"Deliver_boundsHoldAndEvictionOnlyRepeats":                    TestDeliver_boundsHoldAndEvictionOnlyRepeats,
		"Deliver_recordsOnlyADeliveryThroughAHookThatReachesTheAgent": TestDeliver_recordsOnlyADeliveryThroughAHookThatReachesTheAgent,
		"Deliver_neverRecordsALineThatSaysNothingOrTwice":             TestDeliver_neverRecordsALineThatSaysNothingOrTwice,
		"Deliver_needsALaneAndALiveContext":                           TestDeliver_needsALaneAndALiveContext,
		"Unseen_anUnseenRedIsNeverHiddenBehindALaterSeenGreen":        TestUnseen_anUnseenRedIsNeverHiddenBehindALaterSeenGreen,
		"Unseen_isPerActor":                                           TestUnseen_isPerActor,
		"Unseen_dropsARepeatInsideOneCallAndKeepsOrder":               TestUnseen_dropsARepeatInsideOneCallAndKeepsOrder,
		"Handle_keepsDeliveriesAcrossFactsAndQuestions":               TestHandle_keepsDeliveriesAcrossFactsAndQuestions,
		"Deliver_keepsTheNewestPerActorAndTheNewestActors":            TestDeliver_keepsTheNewestPerActorAndTheNewestActors,
		"Guidance_aGuideSeenByThisActorIsNotRepeatedButADenyAlwaysIs": TestGuidance_aGuideSeenByThisActorIsNotRepeatedButADenyAlwaysIs,
		"Deliver_racingDeliveriesAndFactsLoseNoUpdate":                TestDeliver_racingDeliveriesAndFactsLoseNoUpdate,
		"Deliver_givesUpOnALaneThatKeepsChanging":                     TestDeliver_givesUpOnALaneThatKeepsChanging,
		"aQuestionsFlagIsNotAUnitTheNextCommitSeeds":                  TestHandle_aQuestionsFlagIsNotAUnitTheNextCommitSeeds,
		"aFactAboutAFlaggedUnitMovesTheFlagIntoTheUnit":               TestHandle_aFactAboutAFlaggedUnitMovesTheFlagIntoTheUnit,
	} {
		t.Run(name, fn)
	}
}
