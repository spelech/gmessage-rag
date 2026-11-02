# Vision OCR Pipeline (`PIPE-01`)

Shared photos, screenshots, receipts, and calendar invites contain critical conversational context that raw text queries miss.

## Pipeline Lifecycle

1. **Extraction**: When an image attachment is ingested, it is decrypted and saved to `/data/media/`.
2. **Debounce Trigger**: When the per-conversation debouncer fires, the pipeline queries `media_attachments` where `vision_processed = false`.
3. **Multimodal Inference**: The image is formatted into an OpenAI-compatible payload and sent to the configured vision model (e.g. `gpt-4o-mini`).
4. **Structured Output**:
   - `ocr_text`: Verbatim text extracted from signs, documents, screenshots, and receipts.
   - `visual_description`: A concise 1-2 sentence visual description of the image content.
5. **Persistence**: The extracted text is saved back to `media_attachments` and incorporated into transcript headers during session chunking.

## Example Transcript Representation

```text
[2026-03-29 14:12:05 UTC] Sarah: Here is the updated flight itinerary.
[Image Attachment: sha256:7f8a1... | Description: Airline flight confirmation receipt showing departure on April 12th at 8:30 AM from SFO to JFK | OCR: FLIGHT 248 SFO -> JFK DEPART 08:30 SEAT 14B]
[2026-03-29 14:13:00 UTC] Me: Got it, I will add it to the calendar.
```
