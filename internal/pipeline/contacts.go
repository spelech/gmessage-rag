package pipeline

import (
	"strings"

	"github.com/spelech/gmessage-rag/internal/gm"
)

// EnrichStructuredStateWithContacts merges ground-truth Google Contacts metadata into a dossier's
// structured state (PIPE-04). Contacts are matched to the conversation by participant ID or by
// number. The first match becomes the dossier's primary "contact"; every match is retained under
// "contacts". When the conversation carries no participant identifiers, every contact is accepted.
func EnrichStructuredStateWithContacts(
	state map[string]any,
	contacts []gm.ContactSummary,
	participants []string,
) map[string]any {
	if state == nil {
		state = make(map[string]any)
	}
	if len(contacts) == 0 {
		return state
	}

	participantSet := make(map[string]struct{}, len(participants))
	for _, p := range participants {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			participantSet[trimmed] = struct{}{}
		}
	}

	var primary map[string]any
	var matched []any
	for _, contact := range contacts {
		if !contactMatchesConversation(contact, participantSet) {
			continue
		}
		entry := contactDossierEntry(contact)
		matched = append(matched, entry)
		if primary == nil {
			primary = entry
		}
	}

	if primary == nil {
		return state
	}

	state["contact"] = primary
	state["contacts"] = matched
	return state
}

// contactMatchesConversation reports whether a contact belongs to the conversation.
func contactMatchesConversation(contact gm.ContactSummary, participants map[string]struct{}) bool {
	if len(participants) == 0 {
		return true
	}
	if _, ok := participants[strings.TrimSpace(contact.ParticipantID)]; ok && contact.ParticipantID != "" {
		return true
	}
	if _, ok := participants[strings.TrimSpace(contact.Number)]; ok && contact.Number != "" {
		return true
	}
	return false
}

// contactDossierEntry projects a normalized contact into a structured-state entry.
func contactDossierEntry(contact gm.ContactSummary) map[string]any {
	labels := make([]any, 0, len(contact.Labels))
	for _, label := range contact.Labels {
		labels = append(labels, label)
	}

	return map[string]any{
		"contact_id":       contact.ContactID,
		"participant_id":   contact.ParticipantID,
		"name":             contact.Name,
		"nickname":         contact.Nickname,
		"number":           contact.Number,
		"avatar_hex_color": contact.AvatarHexColor,
		"labels":           labels,
	}
}
