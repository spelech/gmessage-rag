package db

import (
	"errors"
	"time"
)

// ErrNotFound is returned when an entity is not found in the database.
var ErrNotFound = errors.New("not found")

// Conversation represents a conversation thread record.
type Conversation struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Participants     []string       `json:"participants"`
	IsGroup          bool           `json:"is_group"`
	IsTracked        bool           `json:"is_tracked"`
	LastMessageAt    *time.Time     `json:"last_message_at,omitempty"`
	BackfillCursor   *string        `json:"backfill_cursor,omitempty"`
	BackfillComplete bool           `json:"backfill_complete"`
	Metadata         map[string]any `json:"metadata"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// Reaction represents an emoji reaction on a message.
type Reaction struct {
	Sender string `json:"sender"`
	Emoji  string `json:"emoji"`
}

// Message represents an individual SMS/RCS message record.
type Message struct {
	ID               string     `json:"id"`
	ConversationID   string     `json:"conversation_id"`
	SessionID        *int64     `json:"session_id,omitempty"`
	SenderID         string     `json:"sender_id"`
	SenderName       string     `json:"sender_name"`
	IsFromMe         bool       `json:"is_from_me"`
	SentAt           time.Time  `json:"sent_at"`
	Body             string     `json:"body"`
	ReplyToMessageID *string    `json:"reply_to_message_id,omitempty"`
	Reactions        []Reaction `json:"reactions"`
	RawStatus        *int32     `json:"raw_status,omitempty"`
	IsBackfill       bool       `json:"is_backfill"`
	CreatedAt        time.Time  `json:"created_at"`
}

// ContactDossier represents a living dossier record for a contact.
type ContactDossier struct {
	ConversationID         string         `json:"conversation_id"`
	ContactName            string         `json:"contact_name"`
	MarkdownContent        string         `json:"markdown_content"`
	StructuredState        map[string]any `json:"structured_state"`
	LastProcessedMessageAt *time.Time     `json:"last_processed_message_at,omitempty"`
	Version                int            `json:"version"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

// ContactDossierHistory represents an audit snapshot of a previous contact dossier state.
type ContactDossierHistory struct {
	ID               int64          `json:"id"`
	ConversationID   string         `json:"conversation_id"`
	Version          int            `json:"version"`
	MarkdownContent  string         `json:"markdown_content"`
	StructuredState  map[string]any `json:"structured_state"`
	TriggerSessionID *int64         `json:"trigger_session_id,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

// MediaAttachment represents an attachment record associated with a message.
type MediaAttachment struct {
	ID                int64     `json:"id"`
	MessageID         string    `json:"message_id"`
	ConversationID    string    `json:"conversation_id"`
	SHA256Hash        string    `json:"sha256_hash"`
	RelativePath      string    `json:"relative_path"`
	MimeType          string    `json:"mime_type"`
	FileSizeBytes     int64     `json:"file_size_bytes"`
	Width             *int      `json:"width,omitempty"`
	Height            *int      `json:"height,omitempty"`
	OCRText           *string   `json:"ocr_text,omitempty"`
	VisualDescription *string   `json:"visual_description,omitempty"`
	VisionProcessed   bool      `json:"vision_processed"`
	CreatedAt         time.Time `json:"created_at"`
}

