package api

import (
	"errors"
	"testing"

	"icomic/backend/gen"
)

func TestUncertainProviderOutcomeRecognizesStructuredSafeError(t *testing.T) {
	if !uncertainProviderOutcome(&gen.AIServiceError{Code: "OUTCOME_UNKNOWN", Message: "The generation outcome could not be confirmed."}) {
		t.Fatal("structured unknown-outcome error must keep the generation uncertain")
	}
	if uncertainProviderOutcome(&gen.AIServiceError{Code: "UPSTREAM_UNAVAILABLE", Message: "Generation service is temporarily unavailable."}) {
		t.Fatal("ordinary upstream failures must not be treated as uncertain")
	}
	if !uncertainProviderOutcome(errors.New("provider outcome is unknown")) {
		t.Fatal("legacy unknown-outcome errors must remain uncertain")
	}
}
