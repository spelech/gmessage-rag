package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/spelech/gmessage-rag/internal/gm"
)

// CookiePairHandler serves the cookie-import pairing UI and API. It replaces
// the old pairing handler. Device pairing uses cookie import.
//
//	GET  /pair              HTML form (or ?format=json for status)
//	POST /pair              accepts {"cookies":"..."} JSON or a cookies form field
type CookiePairHandler struct {
	client *gm.Client

	mu       sync.RWMutex
	emoji    string
	lastErr  string
	inFlight bool
}

// NewCookiePairHandler constructs a cookie pairing handler for the given client.
func NewCookiePairHandler(client *gm.Client) *CookiePairHandler {
	return &CookiePairHandler{client: client}
}

// Status returns the current pairing state for API consumers.
func (h *CookiePairHandler) Status() (paired bool, emoji string, lastErr string, inFlight bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	paired = h.isPairedLocked()
	return paired, h.emoji, h.lastErr, h.inFlight
}

func (h *CookiePairHandler) isPairedLocked() bool {
	if h.client == nil || h.client.LibGM() == nil {
		return false
	}
	return h.client.LibGM().IsLoggedIn()
}

// ServeHTTP implements the cookie pairing form and API.
func (h *CookiePairHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodPost:
		h.handlePost(w, r)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *CookiePairHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("format") == "json" {
		paired, emoji, lastErr, inFlight := h.Status()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"paired":    paired,
			"emoji":     emoji,
			"error":     lastErr,
			"in_flight": inFlight,
		})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, cookiePairPage)
}

func (h *CookiePairHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writePairJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Google Messages client is not configured"})
		return
	}

	h.mu.Lock()
	if h.inFlight {
		h.mu.Unlock()
		writePairJSON(w, http.StatusConflict, map[string]any{"error": "a pairing attempt is already in progress"})
		return
	}
	h.mu.Unlock()

	raw, err := readCookieSubmission(r)
	if err != nil {
		writePairJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	cookies, err := gm.ParseCookies(raw)
	if err != nil {
		writePairJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid cookies: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	h.client.SetCookies(cookies)
	if err := h.client.FetchConfig(ctx); err != nil {
		writePairJSON(w, http.StatusBadRequest, map[string]any{
			"error": "cookies were rejected by Google. Sign in to messages.google.com/web and copy a fresh request as cURL. Details: " + err.Error(),
		})
		return
	}
	if err := h.client.SaveSession(); err != nil {
		writePairJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to save session: " + err.Error()})
		return
	}

	if h.client.HasExistingPairing() {
		_ = h.client.Connect(context.Background())
		h.mu.Lock()
		h.inFlight = false
		h.lastErr = ""
		h.emoji = ""
		h.mu.Unlock()

		writePairJSON(w, http.StatusOK, map[string]any{
			"status":  "paired",
			"paired":  true,
			"message": "Cookies refreshed and existing phone pairing re-authenticated (no emoji challenge needed).",
		})
		return
	}

	h.mu.Lock()
	h.inFlight = true
	h.lastErr = ""
	h.emoji = ""
	h.mu.Unlock()

	go h.startGaia()

	writePairJSON(w, http.StatusOK, map[string]any{
		"status":  "waiting",
		"message": "Cookies accepted. Open Google Messages on your phone and tap the matching emoji.",
	})
}

func (h *CookiePairHandler) startGaia() {
	err := h.client.PairGaia(context.Background(), func(emoji string) {
		h.mu.Lock()
		h.emoji = emoji
		h.mu.Unlock()
	})

	h.mu.Lock()
	h.inFlight = false
	if err != nil {
		h.lastErr = err.Error()
	} else {
		h.emoji = ""
	}
	h.mu.Unlock()
}

func readCookieSubmission(r *http.Request) (string, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var body struct {
			Cookies string `json:"cookies"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body); err != nil {
			return "", fmt.Errorf("invalid JSON body: %w", err)
		}
		return body.Cookies, nil
	}

	if err := r.ParseForm(); err == nil {
		if v := r.FormValue("cookies"); v != "" {
			return v, nil
		}
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read request body: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", fmt.Errorf("no cookies provided")
	}
	return string(data), nil
}

func writePairJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

const cookiePairPage = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Pair Google Messages</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 760px; margin: 40px auto; padding: 0 20px; color: #1e293b; }
    h1 { font-size: 24px; }
    textarea { width: 100%; min-height: 180px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; padding: 10px; border: 1px solid #cbd5e1; border-radius: 8px; box-sizing: border-box; }
    button { margin-top: 12px; padding: 10px 18px; font-size: 14px; font-weight: 600; color: #fff; background: #2563eb; border: 0; border-radius: 8px; cursor: pointer; }
    button:disabled { opacity: 0.6; cursor: not-allowed; }
    .status { margin-top: 16px; color: #475569; }
    .error { color: #dc2626; }
    .emoji { font-size: 56px; margin: 20px 0; }
    ol { line-height: 1.7; }
    code { background: #f1f5f9; padding: 2px 5px; border-radius: 4px; }
  </style>
</head>
<body>
  <h1>Pair Google Messages with cookies</h1>
  <p>Device pairing uses cookie import. Import the cookies from a signed-in browser instead.</p>
  <ol>
    <li>Open <a href="https://messages.google.com/web" target="_blank" rel="noopener">messages.google.com/web</a> and sign in.</li>
    <li>Open DevTools (<code>F12</code>) and switch to the <strong>Network</strong> tab.</li>
    <li>Select a request to <code>messages.google.com</code> (for example <code>config</code>).</li>
    <li>Right-click the request &rarr; <strong>Copy</strong> &rarr; <strong>Copy as cURL</strong>.</li>
    <li>Paste the whole cURL command below and click <strong>Pair</strong>.</li>
  </ol>
  <textarea id="cookies" placeholder="Paste the cURL command here"></textarea>
  <div><button id="pair" type="button">Pair with cookies</button></div>
  <div id="status" class="status"></div>
  <div id="emoji" class="emoji"></div>
  <script>
    const btn = document.getElementById('pair');
    const statusEl = document.getElementById('status');
    const emojiEl = document.getElementById('emoji');
    let timer = null;

    btn.addEventListener('click', async () => {
      const cookies = document.getElementById('cookies').value.trim();
      if (!cookies) { statusEl.textContent = 'Paste cookies first.'; return; }
      btn.disabled = true;
      statusEl.className = 'status';
      statusEl.textContent = 'Validating cookies...';
      try {
        const res = await fetch('/pair', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ cookies })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || 'Pairing failed');
        statusEl.textContent = data.message || 'Cookies accepted. Watch for the emoji.';
        poll();
      } catch (err) {
        statusEl.className = 'status error';
        statusEl.textContent = 'Error: ' + err.message;
        btn.disabled = false;
      }
    });

    async function poll() {
      try {
        const res = await fetch('/pair?format=json');
        const data = await res.json();
        if (data.emoji) emojiEl.textContent = data.emoji;
        if (data.paired) {
          statusEl.className = 'status';
          statusEl.textContent = 'Paired! You can close this page.';
          emojiEl.textContent = '';
          btn.disabled = true;
          return;
        }
        if (data.error) {
          statusEl.className = 'status error';
          statusEl.textContent = 'Error: ' + data.error;
          btn.disabled = false;
          return;
        }
        timer = setTimeout(poll, 3000);
      } catch (err) {
        timer = setTimeout(poll, 3000);
      }
    }
  </script>
</body>
</html>
`
