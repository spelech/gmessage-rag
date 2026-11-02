package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Requirement: TEST-04 (controls-grade test harness & UI layout audit)

type mockContact struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	IsGroup     bool    `json:"is_group"`
	LastMessage *string `json:"last_message_at"`
	IsTracked   bool    `json:"is_tracked"`
}

type mockDashboardBackend struct {
	mu           sync.Mutex
	statusCalled bool
	contacts     []mockContact
	trackCalls   []TrackContactRequest
}

func (m *mockDashboardBackend) HandleStatus(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.statusCalled = true
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(StatusResponse{
		Status:            "healthy",
		DBConnected:       true,
		GMConnected:       true,
		UnindexedMessages: 42,
	})
}

func (m *mockDashboardBackend) HandleContacts(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m.contacts)
}

func (m *mockDashboardBackend) HandleTrackContact(w http.ResponseWriter, r *http.Request) {
	var req TrackContactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.trackCalls = append(m.trackCalls, req)
	for i := range m.contacts {
		if m.contacts[i].ID == req.ConversationID {
			m.contacts[i].IsTracked = req.IsTracked
		}
	}
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"conversation_id": req.ConversationID,
		"is_tracked":      req.IsTracked,
		"success":         true,
	})
}

// checkChromiumAvailable checks if chrome/chromium exists in PATH.
func checkChromiumAvailable() bool {
	candidates := []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"}
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			return true
		}
	}
	return false
}

func TestLayoutAudit_HeadlessChrome(t *testing.T) {
	if !checkChromiumAvailable() {
		t.Skip("Chromium not available for headless UI audit")
	}

	// Setup mock backend with realistic contacts
	backend := &mockDashboardBackend{
		contacts: []mockContact{
			{
				ID:        "conv-alice",
				Name:      "Alice Smith",
				IsGroup:   false,
				IsTracked: false,
			},
			{
				ID:        "conv-bob",
				Name:      "Bob Jones",
				IsGroup:   true,
				IsTracked: true,
			},
		},
	}

	// Serve the real embedded dashboard assets while intercepting API calls with mock backend
	dashboardHTML, err := dashboardAssets.ReadFile("assets/index.html")
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status", backend.HandleStatus)
	mux.HandleFunc("/api/v1/contacts", backend.HandleContacts)
	mux.HandleFunc("/api/v1/contacts/track", backend.HandleTrackContact)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboardHTML)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Ensure output screenshot directory exists
	// Project root assets/screenshots/
	rootDir, err := filepath.Abs("../..")
	require.NoError(t, err)
	screenshotDir := filepath.Join(rootDir, "assets", "screenshots")
	err = os.MkdirAll(screenshotDir, 0755)
	require.NoError(t, err)

	viewports := []struct {
		name      string
		width     int64
		height    int64
		isMobile  bool
		minTarget float64
	}{
		{
			name:      "mobile",
			width:     375,
			height:    667,
			isMobile:  true,
			minTarget: 44.0,
		},
		{
			name:      "tablet",
			width:     768,
			height:    1024,
			isMobile:  false,
			minTarget: 24.0,
		},
		{
			name:      "desktop",
			width:     1440,
			height:    900,
			isMobile:  false,
			minTarget: 24.0,
		},
	}

	// Create Allocator with headless Chrome options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.Headless,
		chromedp.DisableGPU,
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	for _, vp := range viewports {
		t.Run("Viewport_"+vp.name, func(t *testing.T) {
			ctx, cancel := chromedp.NewContext(allocCtx)
			defer cancel()

			ctx, timeoutCancel := context.WithTimeout(ctx, 30*time.Second)
			defer timeoutCancel()

			var vpOpts []chromedp.EmulateViewportOption
			if vp.isMobile {
				vpOpts = append(vpOpts, chromedp.EmulateMobile, chromedp.EmulatePortrait)
			} else {
				vpOpts = append(vpOpts, chromedp.EmulatePortrait)
			}

			err := chromedp.Do(ctx,
				chromedp.EmulateViewport(vp.width, vp.height, vpOpts...),
				chromedp.Navigate(server.URL),
				chromedp.WaitVisible(`#contacts-table-body button`),
			)
			require.NoError(t, err)

			// 1. Horizontal Viewport Overflow check:
			// Assert document.documentElement.scrollWidth <= window.innerWidth across all tested viewports
			scrollWidth, err := chromedp.Run(ctx, chromedp.Evaluate[int64](`document.documentElement.scrollWidth`))
			require.NoError(t, err)

			innerWidth, err := chromedp.Run(ctx, chromedp.Evaluate[int64](`window.innerWidth`))
			require.NoError(t, err)

			assert.LessOrEqual(t, scrollWidth, innerWidth, "viewport horizontal overflow detected on %s: scrollWidth=%d > innerWidth=%d", vp.name, scrollWidth, innerWidth)

			// 2. Touch Target Sizing check:
			// Query all interactive buttons and inputs (button, input, a) and assert bounding box dimensions >= 24px (and >= 44px for mobile tap targets).
			type elemBox struct {
				Tag    string  `json:"tag"`
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
				Text   string  `json:"text"`
			}
			const evalElementsScript = `
			(function() {
				return Array.from(document.querySelectorAll('button, input, a')).filter(function(el) {
					var rect = el.getBoundingClientRect();
					var style = window.getComputedStyle(el);
					return rect.width > 0 && rect.height > 0 && style.visibility !== 'hidden' && style.display !== 'none';
				}).map(function(el) {
					var rect = el.getBoundingClientRect();
					return {
						tag: el.tagName.toLowerCase(),
						width: rect.width,
						height: rect.height,
						text: (el.innerText || el.value || '').trim()
					};
				});
			})()
			`
			targets, err := chromedp.Run(ctx, chromedp.Evaluate[[]elemBox](evalElementsScript))
			require.NoError(t, err)
			assert.NotEmpty(t, targets, "should find interactive elements on dashboard")

			for _, target := range targets {
				assert.GreaterOrEqual(t, target.Width, vp.minTarget, "touch target %s ('%s') width %f < %f on %s", target.Tag, target.Text, target.Width, vp.minTarget, vp.name)
				assert.GreaterOrEqual(t, target.Height, vp.minTarget, "touch target %s ('%s') height %f < %f on %s", target.Tag, target.Text, target.Height, vp.minTarget, vp.name)
			}

			// 3. Visual Regression Screenshots:
			// Capture real rendered screenshots (chromedp.FullScreenshot) in both light/dark contexts and save to assets/screenshots/
			themes := []struct {
				mode    string
				bgColor string
			}{
				{mode: "dark", bgColor: "#0f172a"},
				{mode: "light", bgColor: "#f8fafc"},
			}

			for _, th := range themes {
				prepareScript := `
				if ("` + th.mode + `" === "light") {
					document.documentElement.style.setProperty('--bg', '#f8fafc');
					document.documentElement.style.setProperty('--card-bg', '#ffffff');
					document.documentElement.style.setProperty('--text', '#0f172a');
					document.documentElement.style.setProperty('--text-muted', '#64748b');
					document.documentElement.style.setProperty('--border', '#e2e8f0');
				} else {
					document.documentElement.style.setProperty('--bg', '#0f172a');
					document.documentElement.style.setProperty('--card-bg', '#1e293b');
					document.documentElement.style.setProperty('--text', '#f8fafc');
					document.documentElement.style.setProperty('--text-muted', '#94a3b8');
					document.documentElement.style.setProperty('--border', '#334155');
				}
				`
				err = chromedp.Do(ctx,
					chromedp.Evaluate[chromedp.Void](prepareScript),
					chromedp.Sleep(50*time.Millisecond),
				)
				require.NoError(t, err)

				buf, err := chromedp.Run(ctx, chromedp.FullScreenshot(100))
				require.NoError(t, err)

				screenshotFile := filepath.Join(screenshotDir, "dashboard_"+vp.name+"_"+th.mode+".png")
				err = os.WriteFile(screenshotFile, buf, 0644)
				require.NoError(t, err)

				fi, err := os.Stat(screenshotFile)
				require.NoError(t, err)
				assert.Greater(t, fi.Size(), int64(0), "screenshot file should not be empty")
			}
		})
	}

	// 4. DOM & State Interactivity:
	// Verify that clicking tracking buttons properly updates UI state and dispatches POST /api/v1/contacts/track
	t.Run("Interactivity_ToggleTrack", func(t *testing.T) {
		ctx, cancel := chromedp.NewContext(allocCtx)
		defer cancel()

		ctx, timeoutCancel := context.WithTimeout(ctx, 30*time.Second)
		defer timeoutCancel()

		err := chromedp.Do(ctx,
			chromedp.Navigate(server.URL),
			chromedp.WaitVisible(`#contacts-table-body tr`),
		)
		require.NoError(t, err)

		// Check initial button text of the first contact (conv-alice, Untracked)
		btnTextBefore, err := chromedp.Run(ctx, chromedp.Text(`#contacts-table-body tr:first-child button`))
		require.NoError(t, err)
		assert.Equal(t, "Untracked", btnTextBefore)

		// Click the first button and wait for update
		err = chromedp.Do(ctx,
			chromedp.Click(`#contacts-table-body tr:first-child button`),
			chromedp.WaitVisible(`#contacts-table-body tr:first-child button.btn-track`),
		)
		require.NoError(t, err)

		btnTextAfter, err := chromedp.Run(ctx, chromedp.Text(`#contacts-table-body tr:first-child button`))
		require.NoError(t, err)
		assert.Equal(t, "Tracked", btnTextAfter)

		// Verify backend recorded the POST /api/v1/contacts/track request
		backend.mu.Lock()
		defer backend.mu.Unlock()
		require.NotEmpty(t, backend.trackCalls)
		lastCall := backend.trackCalls[len(backend.trackCalls)-1]
		assert.Equal(t, "conv-alice", lastCall.ConversationID)
		assert.True(t, lastCall.IsTracked)
	})
}
