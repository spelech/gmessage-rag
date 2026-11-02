# Runtime Parameter Tuner (`UI-03`)

The parameter tuner allows operators to adjust pipeline heuristics dynamically without restarting the daemon.

## Tunable Parameters

- **Idle Debouncer Window**: Change the conversational idle timeout from 5 to 120 minutes.
- **RRF $k$ Smoothing Factor**: Adjust the rank weighting between vector similarity and full-text search.
- **Backfill Batch Size**: Control pagination limits during historical backfill.
- **Vision OCR Endpoint**: Point to local or alternate vision inference providers.

## Thread-Safe Mutation Architecture

- Backed by `internal/config/tuner.go` using a thread-safe `sync.RWMutex`.
- Emits broadcast updates over Go channels to notify active workers immediately.
