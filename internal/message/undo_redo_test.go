package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// seedTurn creates a user message followed by an assistant reply and
// returns both.
func seedTurn(t *testing.T, svc Service, sessionID, prompt, reply string) (Message, Message) {
	t.Helper()
	user, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: prompt}},
	})
	require.NoError(t, err)

	assistant, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  Assistant,
		Parts: []ContentPart{TextContent{Text: reply}},
	})
	require.NoError(t, err)
	return user, assistant
}

func TestDeleteMessagesAfterRemovesTurnAndEverythingLater(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	first, _ := seedTurn(t, svc, sessionID, "first", "first reply")
	second, _ := seedTurn(t, svc, sessionID, "second", "second reply")

	before, err := svc.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, before, 4)

	require.NoError(t, svc.DeleteMessagesAfter(t.Context(), sessionID, second.ID))

	after, err := svc.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, after, 2)
	require.Equal(t, first.ID, after[0].ID)
	require.NotEqual(t, second.ID, after[1].ID)
}

func TestRestoreMessagesRoundTripsDeletedTurn(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	_, _ = seedTurn(t, svc, sessionID, "first", "first reply")
	lastUser, _ := seedTurn(t, svc, sessionID, "second", "second reply")

	all, err := svc.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, all, 4)

	var deleted []Message
	for i, msg := range all {
		if msg.ID == lastUser.ID {
			deleted = append(deleted, all[i:]...)
			break
		}
	}
	require.Len(t, deleted, 2)

	require.NoError(t, svc.DeleteMessagesAfter(t.Context(), sessionID, lastUser.ID))
	truncated, err := svc.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, truncated, 2)

	require.NoError(t, svc.RestoreMessages(t.Context(), deleted))

	restored, err := svc.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, restored, 4)
	require.Equal(t, all, restored)
}

func TestRestoreMessagesIsIdempotent(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	user, _ := seedTurn(t, svc, sessionID, "only", "only reply")

	all, err := svc.List(t.Context(), sessionID)
	require.NoError(t, err)

	require.NoError(t, svc.DeleteMessagesAfter(t.Context(), sessionID, user.ID))
	require.NoError(t, svc.RestoreMessages(t.Context(), all))
	require.NoError(t, svc.RestoreMessages(t.Context(), all))

	restored, err := svc.List(t.Context(), sessionID)
	require.NoError(t, err)
	require.Len(t, restored, 2)
}
