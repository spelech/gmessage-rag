package obs_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spelech/gmessage-rag/internal/obs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_InitializationAndRegistry(t *testing.T) {
	m := obs.NewMetrics()
	require.NotNil(t, m)
	assert.NotNil(t, m.Registry())
	assert.NotNil(t, m.Handler())

	defaultM := obs.DefaultMetrics()
	require.NotNil(t, defaultM)
	assert.Same(t, defaultM, obs.DefaultMetrics())
}

func TestMetrics_MutationsAndCounters(t *testing.T) {
	m := obs.NewMetrics()

	// Ingested messages
	m.RecordMessageIngested(obs.DirectionInbound, obs.ProtocolSMS)
	m.RecordMessageIngested(obs.DirectionInbound, obs.ProtocolSMS)
	m.RecordMessageIngested(obs.DirectionOutbound, obs.ProtocolRCS)
	m.RecordMessageIngested(obs.DirectionInbound, obs.ProtocolMMS)

	// Decrypted media bytes
	m.RecordMediaBytesDecrypted(1024)
	m.RecordMediaBytesDecrypted(2048)
	m.RecordMediaBytesDecrypted(-500) // Negative bytes should be ignored

	// Active websocket connections
	m.SetActiveWebsocketConnections(3)
	m.IncActiveWebsocketConnections()
	m.DecActiveWebsocketConnections()

	// Session chunks created
	m.RecordSessionChunkCreated()
	m.RecordSessionChunkCreated()

	// Verify metrics via Prometheus testutil on individual collectors
	assert.Equal(t, float64(2), testutil.ToFloat64(m.MessagesIngestedTotal().WithLabelValues(obs.DirectionInbound, obs.ProtocolSMS)))
	assert.Equal(t, float64(1), testutil.ToFloat64(m.MessagesIngestedTotal().WithLabelValues(obs.DirectionOutbound, obs.ProtocolRCS)))
	assert.Equal(t, float64(1), testutil.ToFloat64(m.MessagesIngestedTotal().WithLabelValues(obs.DirectionInbound, obs.ProtocolMMS)))
	assert.Equal(t, float64(3072), testutil.ToFloat64(m.MediaBytesDecryptedTotal()))
	assert.Equal(t, float64(3), testutil.ToFloat64(m.ActiveWebsocketConnections()))
	assert.Equal(t, float64(2), testutil.ToFloat64(m.SessionChunksCreatedTotal()))
}

func TestMetrics_HTTPEndpointScrape(t *testing.T) {
	m := obs.NewMetrics()

	m.RecordMessageIngested(obs.DirectionInbound, obs.ProtocolRCS)
	m.RecordMediaBytesDecrypted(4096)
	m.SetActiveWebsocketConnections(2)
	m.RecordSessionChunkCreated()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	m.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)

	// Assert metric representations in response
	assert.True(t, strings.Contains(bodyStr, obs.MetricMessagesIngestedTotal), "expected messages ingested metric in body")
	assert.True(t, strings.Contains(bodyStr, `direction="inbound"`), "expected direction label")
	assert.True(t, strings.Contains(bodyStr, `protocol="rcs"`), "expected protocol label")
	assert.True(t, strings.Contains(bodyStr, obs.MetricMediaBytesDecryptedTotal), "expected media bytes metric")
	assert.True(t, strings.Contains(bodyStr, "4096"), "expected 4096 media bytes")
	assert.True(t, strings.Contains(bodyStr, obs.MetricActiveWebsocketConnections), "expected websocket connections gauge")
	assert.True(t, strings.Contains(bodyStr, obs.MetricSessionChunksCreatedTotal), "expected session chunks counter")
}

func TestMetrics_ConcurrencyAndRaceSafety(t *testing.T) {
	m := obs.NewMetrics()

	var wg sync.WaitGroup
	workers := 20
	iterations := 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if id%2 == 0 {
					m.RecordMessageIngested(obs.DirectionInbound, obs.ProtocolRCS)
					m.IncActiveWebsocketConnections()
				} else {
					m.RecordMessageIngested(obs.DirectionOutbound, obs.ProtocolSMS)
					m.DecActiveWebsocketConnections()
				}
				m.RecordMediaBytesDecrypted(100)
				m.RecordSessionChunkCreated()
			}
		}(i)
	}

	wg.Wait()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMetrics_NilSafety(t *testing.T) {
	var m *obs.Metrics

	// Ensure calling methods on nil metrics does not panic
	assert.NotPanics(t, func() {
		m.RecordMessageIngested(obs.DirectionInbound, obs.ProtocolSMS)
		m.RecordMediaBytesDecrypted(1024)
		m.SetActiveWebsocketConnections(1)
		m.IncActiveWebsocketConnections()
		m.DecActiveWebsocketConnections()
		m.RecordSessionChunkCreated()
	})
}
