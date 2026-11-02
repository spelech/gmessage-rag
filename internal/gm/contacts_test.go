package gm

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

type mockContactsFetcher struct {
	response *gmproto.ListContactsResponse
	err      error
}

func (m *mockContactsFetcher) ListContacts(ctx context.Context) (*gmproto.ListContactsResponse, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// Requirement ID: PIPE-04
func TestSyncContacts_Nominal(t *testing.T) {
	fetcher := &mockContactsFetcher{
		response: &gmproto.ListContactsResponse{
			Contacts: []*gmproto.Contact{
				{
					ContactID:      "c-1",
					ParticipantID:  "p-1",
					Name:           "Alice Smith",
					AvatarHexColor: "#FF5733",
					Number: &gmproto.ContactNumber{
						Number: "+15551234567",
					},
				},
				{
					ContactID:      "c-2",
					ParticipantID:  "p-2",
					NameAgain:      "Bob Jones",
					AvatarHexColor: "#33FF57",
					Number: &gmproto.ContactNumber{
						Number: "+15559876543",
					},
				},
			},
		},
	}

	ctx := context.Background()
	summaries, err := SyncContacts(ctx, fetcher)
	require.NoError(t, err)
	require.Len(t, summaries, 2)

	assert.Equal(t, "c-1", summaries[0].ContactID)
	assert.Equal(t, "p-1", summaries[0].ParticipantID)
	assert.Equal(t, "Alice Smith", summaries[0].Name)
	assert.Equal(t, "+15551234567", summaries[0].Number)
	assert.Equal(t, "#FF5733", summaries[0].AvatarHexColor)

	assert.Equal(t, "c-2", summaries[1].ContactID)
	assert.Equal(t, "p-2", summaries[1].ParticipantID)
	assert.Equal(t, "Bob Jones", summaries[1].Name)
	assert.Equal(t, "+15559876543", summaries[1].Number)
	assert.Equal(t, "#33FF57", summaries[1].AvatarHexColor)
}

// Requirement ID: PIPE-04
func TestSyncContacts_NilFetcher(t *testing.T) {
	ctx := context.Background()
	summaries, err := SyncContacts(ctx, nil)
	assert.Error(t, err)
	assert.Nil(t, summaries)
	assert.Contains(t, err.Error(), "contacts fetcher cannot be nil")
}

// Requirement ID: PIPE-04
func TestSyncContacts_FetcherError(t *testing.T) {
	fetcher := &mockContactsFetcher{
		err: errors.New("network timeout"),
	}

	ctx := context.Background()
	summaries, err := SyncContacts(ctx, fetcher)
	assert.Error(t, err)
	assert.Nil(t, summaries)
	assert.Contains(t, err.Error(), "failed to list contacts")
}

// Requirement ID: PIPE-04
func TestSyncContacts_NilResponse(t *testing.T) {
	fetcher := &mockContactsFetcher{
		response: nil,
	}

	ctx := context.Background()
	summaries, err := SyncContacts(ctx, fetcher)
	require.NoError(t, err)
	assert.Empty(t, summaries)
}

// Requirement ID: PIPE-04
func TestSyncContacts_EmptyContactsList(t *testing.T) {
	fetcher := &mockContactsFetcher{
		response: &gmproto.ListContactsResponse{
			Contacts: []*gmproto.Contact{},
		},
	}

	ctx := context.Background()
	summaries, err := SyncContacts(ctx, fetcher)
	require.NoError(t, err)
	assert.Empty(t, summaries)
}

// Requirement ID: PIPE-04
func TestSyncContacts_NilFieldsAndNilEntries(t *testing.T) {
	fetcher := &mockContactsFetcher{
		response: &gmproto.ListContactsResponse{
			Contacts: []*gmproto.Contact{
				nil,
				{
					ContactID:      "c-nil-num",
					ParticipantID:  "p-nil-num",
					Name:           "No Number Contact",
					AvatarHexColor: "",
					Number:         nil,
				},
				nil,
				{
					ContactID:     "c-sparse",
					ParticipantID: "",
					Name:          "",
					Number: &gmproto.ContactNumber{
						Number: "",
					},
				},
			},
		},
	}

	ctx := context.Background()
	summaries, err := SyncContacts(ctx, fetcher)
	require.NoError(t, err)
	require.Len(t, summaries, 2)

	assert.Equal(t, "c-nil-num", summaries[0].ContactID)
	assert.Equal(t, "p-nil-num", summaries[0].ParticipantID)
	assert.Equal(t, "No Number Contact", summaries[0].Name)
	assert.Equal(t, "", summaries[0].Number)
	assert.Equal(t, "", summaries[0].AvatarHexColor)

	assert.Equal(t, "c-sparse", summaries[1].ContactID)
	assert.Equal(t, "", summaries[1].ParticipantID)
	assert.Equal(t, "", summaries[1].Name)
	assert.Equal(t, "", summaries[1].Number)
}

// Requirement ID: PIPE-04
func TestSyncContacts_ContextCancellation(t *testing.T) {
	fetcher := &mockContactsFetcher{
		response: &gmproto.ListContactsResponse{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	summaries, err := SyncContacts(ctx, fetcher)
	assert.Error(t, err)
	assert.Nil(t, summaries)
}
