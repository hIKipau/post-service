package postgresql

import (
	"testing"

	"post-service/internal/domain"
)

// TestReactionDelta covers every state transition, including repeated identical states and removals.
func TestReactionDelta(t *testing.T) {
	for _, before := range []domain.ReactionKind{domain.ReactionNone, domain.ReactionLike, domain.ReactionDislike} {
		for _, after := range []domain.ReactionKind{domain.ReactionNone, domain.ReactionLike, domain.ReactionDislike} {
			for _, counted := range []domain.ReactionKind{domain.ReactionLike, domain.ReactionDislike} {
				var oldCount, newCount int64
				if before == counted {
					oldCount = 1
				}
				if after == counted {
					newCount = 1
				}
				if got := reactionDelta(before, after, counted); got != newCount-oldCount {
					t.Fatalf("%d -> %d count=%d delta=%d", before, after, counted, got)
				}
			}
		}
	}
}
