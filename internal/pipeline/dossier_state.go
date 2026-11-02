package pipeline

import (
	"fmt"
	"strings"
	"time"
)

// dossierEventKeys lists the structured-state arrays that hold event-like entries.
var dossierEventKeys = []string{"upcoming_events", "past_events", "events"}

// dossierLoopKeys lists the structured-state arrays that may carry relative due dates.
var dossierLoopKeys = []string{"open_loops"}

// asMap returns v as a string-keyed map when it is one, else nil.
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// asMapSlice normalizes a JSON-decoded array of objects into []map[string]any while
// preserving the underlying map references so callers can mutate entries in place.
func asMapSlice(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

// toAnySlice re-wraps a []map[string]any as []any so the value round-trips through JSONB.
func toAnySlice(items []map[string]any) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}

// dateOnly truncates t to a UTC midnight calendar date.
func dateOnly(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// AnchorRelativeDates resolves relative-date phrases (for example "tomorrow" or "next Tuesday")
// into explicit ISO-8601 calendar dates using anchor, the timestamp of the newest session.
// Anchoring is deterministic and does not depend on the model (PIPE-03).
func AnchorRelativeDates(state map[string]any, anchor time.Time) {
	if state == nil {
		return
	}
	keys := append(append([]string{}, dossierEventKeys...), dossierLoopKeys...)
	for _, key := range keys {
		for _, item := range asMapSlice(state[key]) {
			rel, _ := item["relative_date"].(string)
			if strings.TrimSpace(rel) == "" {
				continue
			}
			if d, ok := resolveRelativeDate(rel, anchor); ok {
				item["date"] = d.Format("2006-01-02")
				item["anchored_at"] = anchor.UTC().Format(time.RFC3339)
			}
		}
	}
}

// resolveRelativeDate converts a relative phrase to a concrete UTC date anchored at base.
func resolveRelativeDate(phrase string, base time.Time) (time.Time, bool) {
	normalized := strings.ToLower(strings.TrimSpace(phrase))
	normalized = strings.TrimPrefix(normalized, "on ")
	normalized = strings.TrimPrefix(normalized, "the ")
	normalized = strings.TrimSuffix(normalized, ".")
	base = dateOnly(base)

	switch normalized {
	case "today", "tonight", "this morning", "this afternoon", "this evening":
		return base, true
	case "tomorrow":
		return base.AddDate(0, 0, 1), true
	case "yesterday":
		return base.AddDate(0, 0, -1), true
	case "next week", "in a week":
		return base.AddDate(0, 0, 7), true
	case "next month", "in a month":
		return base.AddDate(0, 1, 0), true
	}

	// "in N days" / "+N days"
	if days, ok := parseRelativeOffsets(normalized); ok {
		return base.AddDate(0, 0, days), true
	}

	if d, ok := resolveWeekday(normalized, base); ok {
		return d, true
	}

	return time.Time{}, false
}

// parseRelativeOffsets handles "in N day(s)" and "+N day(s)" phrasing.
func parseRelativeOffsets(phrase string) (int, bool) {
	phrase = strings.TrimPrefix(phrase, "in ")
	phrase = strings.TrimPrefix(phrase, "+")
	fields := strings.Fields(phrase)
	if len(fields) != 2 || !strings.HasPrefix(fields[1], "day") {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(fields[0], "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

// resolveWeekday maps a weekday name (optionally prefixed with "next" or "this") to its next
// calendar occurrence strictly after base.
func resolveWeekday(phrase string, base time.Time) (time.Time, bool) {
	explicitNext := false
	for _, prefix := range []string{"next ", "this ", "coming "} {
		if strings.HasPrefix(phrase, prefix) {
			explicitNext = prefix == "next "
			phrase = strings.TrimPrefix(phrase, prefix)
			break
		}
	}

	weekdays := map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
		"wednesday": time.Wednesday, "thursday": time.Thursday,
		"friday": time.Friday, "saturday": time.Saturday,
	}
	target, ok := weekdays[phrase]
	if !ok {
		return time.Time{}, false
	}

	ahead := int(target - base.Weekday())
	if ahead < 0 {
		ahead += 7
	}
	if ahead == 0 || explicitNext {
		ahead += 7
	}

	return base.AddDate(0, 0, ahead), true
}

// ApplyChronologicalOverrides removes event and open-loop entries whose id the model reported as
// superseded by newer facts, retaining the rest as historical context (PIPE-03).
func ApplyChronologicalOverrides(state map[string]any, supersedes []string) {
	if state == nil || len(supersedes) == 0 {
		return
	}

	drop := make(map[string]struct{}, len(supersedes))
	for _, s := range supersedes {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			drop[trimmed] = struct{}{}
		}
	}
	if len(drop) == 0 {
		return
	}

	keys := append(append([]string{}, dossierEventKeys...), dossierLoopKeys...)
	for _, key := range keys {
		items := asMapSlice(state[key])
		if len(items) == 0 {
			continue
		}
		kept := make([]map[string]any, 0, len(items))
		for _, item := range items {
			id, _ := item["id"].(string)
			if _, superseded := drop[strings.TrimSpace(id)]; superseded {
				continue
			}
			kept = append(kept, item)
		}
		state[key] = toAnySlice(kept)
	}
}

// TransitionPastEvents moves anchored events whose date lies before now from upcoming_events into
// past_events and marks them occurred (PIPE-03).
func TransitionPastEvents(state map[string]any, now time.Time) {
	if state == nil {
		return
	}

	upcoming := asMapSlice(state["upcoming_events"])
	if len(upcoming) == 0 {
		return
	}

	today := dateOnly(now)
	past := asMapSlice(state["past_events"])
	remaining := make([]map[string]any, 0, len(upcoming))

	for _, event := range upcoming {
		if d, ok := eventDate(event); ok && d.Before(today) {
			event["status"] = "occurred"
			past = append(past, event)
			continue
		}
		remaining = append(remaining, event)
	}

	state["upcoming_events"] = toAnySlice(remaining)
	state["past_events"] = toAnySlice(past)
}

// eventDate parses an event's ISO-8601 date field.
func eventDate(event map[string]any) (time.Time, bool) {
	raw, _ := event["date"].(string)
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	d, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return d, true
}

// RenderDossierMarkdown serializes the reconciled structured state into canonical Markdown so the
// exported file always reflects anchored dates and contact enrichment (PIPE-03 / PIPE-04).
func RenderDossierMarkdown(contactName string, state map[string]any, narrative string) string {
	var b strings.Builder

	title := strings.TrimSpace(contactName)
	if title == "" {
		title = "Unknown Contact"
	}
	fmt.Fprintf(&b, "# Living Dossier: %s\n\n", title)

	if contact := asMap(state["contact"]); contact != nil {
		if name, _ := contact["name"].(string); strings.TrimSpace(name) != "" {
			fmt.Fprintf(&b, "**Contact:** %s\n", name)
		}
		if nick, _ := contact["nickname"].(string); strings.TrimSpace(nick) != "" {
			fmt.Fprintf(&b, "**Nickname:** %s\n", nick)
		}
		if avatar, _ := contact["avatar_hex_color"].(string); strings.TrimSpace(avatar) != "" {
			fmt.Fprintf(&b, "**Avatar:** `%s`\n", avatar)
		}
		if labels := stringSlice(contact["labels"]); len(labels) > 0 {
			fmt.Fprintf(&b, "**Labels:** %s\n", strings.Join(labels, ", "))
		}
		b.WriteString("\n")
	}

	if narrative = strings.TrimSpace(narrative); narrative != "" {
		b.WriteString(narrative)
		b.WriteString("\n\n")
	}

	writeEventSection(&b, "Upcoming Events", state["upcoming_events"])
	writeEventSection(&b, "Past Events", state["past_events"])
	writeEventSection(&b, "Open Loops", state["open_loops"])

	return b.String()
}

// writeEventSection appends a Markdown bullet list for an event-like array.
func writeEventSection(b *strings.Builder, heading string, raw any) {
	items := asMapSlice(raw)
	if len(items) == 0 {
		return
	}

	fmt.Fprintf(b, "## %s\n", heading)
	for _, item := range items {
		title, _ := item["title"].(string)
		if strings.TrimSpace(title) == "" {
			title, _ = item["name"].(string)
		}
		line := strings.TrimSpace(title)
		if line == "" {
			line = "(untitled)"
		}

		if date, _ := item["date"].(string); strings.TrimSpace(date) != "" {
			line += " — " + date
		}
		if details, _ := item["details"].(string); strings.TrimSpace(details) != "" {
			line += " (" + details + ")"
		}
		if status, _ := item["status"].(string); strings.TrimSpace(status) != "" {
			line += " [" + status + "]"
		}

		fmt.Fprintf(b, "- %s\n", line)
	}
	b.WriteString("\n")
}

// stringSlice normalizes a JSON-decoded string array.
func stringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
