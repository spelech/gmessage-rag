package gm

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// ContactsFetcher abstracts fetching Google Contacts from libgm.
type ContactsFetcher interface {
	ListContacts(ctx context.Context) (*gmproto.ListContactsResponse, error)
}

// ListContacts implements ContactsFetcher by forwarding to the underlying libgm client.
func (c *Client) ListContacts(ctx context.Context) (*gmproto.ListContactsResponse, error) {
	return c.libgmClient.ListContacts(ctx)
}

// ContactThumbnailFetcher optionally provides contact thumbnail fetching.
type ContactThumbnailFetcher interface {
	GetContactThumbnail(ctx context.Context, contactIDs ...string) (*gmproto.GetThumbnailResponse, error)
}

// ContactSummary represents a normalized Google Contact entry.
type ContactSummary struct {
	ContactID      string `json:"contact_id"`
	ParticipantID  string `json:"participant_id"`
	Name           string `json:"name"`
	Number         string `json:"number"`
	AvatarHexColor string `json:"avatar_hex_color"`
}

// SyncContacts retrieves and normalizes contacts from the given ContactsFetcher.
// Handles nil fetcher, fetch errors, nil response, empty contacts list, and nil fields safely.
func SyncContacts(ctx context.Context, fetcher ContactsFetcher) ([]ContactSummary, error) {
	if fetcher == nil {
		return nil, errors.New("contacts fetcher cannot be nil")
	}

	resp, err := fetcher.ListContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list contacts: %w", err)
	}
	if resp == nil {
		return []ContactSummary{}, nil
	}

	rawContacts := resp.GetContacts()
	if len(rawContacts) == 0 {
		return []ContactSummary{}, nil
	}

	summaries := make([]ContactSummary, 0, len(rawContacts))
	for _, c := range rawContacts {
		if c == nil {
			continue
		}

		var number string
		if num := c.GetNumber(); num != nil {
			number = num.GetNumber()
		}

		name := c.GetName()
		if name == "" {
			name = c.GetNameAgain()
		}

		summaries = append(summaries, ContactSummary{
			ContactID:      c.GetContactID(),
			ParticipantID:  c.GetParticipantID(),
			Name:           name,
			Number:         number,
			AvatarHexColor: c.GetAvatarHexColor(),
		})
	}

	return summaries, nil
}
