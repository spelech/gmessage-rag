package gm

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSID     = "sid-value"
	testHSID    = "hsid-value"
	testOSID    = "osid-value"
	testSSID    = "ssid-value"
	testAPISID  = "apisid-value"
	testSAPISID = "sapisid-value"
)

func requiredCookieMap() map[string]string {
	return map[string]string{
		"SID":     testSID,
		"HSID":    testHSID,
		"OSID":    testOSID,
		"SSID":    testSSID,
		"APISID":  testAPISID,
		"SAPISID": testSAPISID,
	}
}

func TestParseCookies(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr string
	}{
		{
			name:  "flat JSON object",
			input: `{"SID":"sid-value","HSID":"hsid-value","OSID":"osid-value","SSID":"ssid-value","APISID":"apisid-value","SAPISID":"sapisid-value"}`,
			want:  requiredCookieMap(),
		},
		{
			name:  "JSON wrapper with cookies map",
			input: `{"url":"https://messages.google.com/web/config","cookies":{"SID":"sid-value","HSID":"hsid-value","OSID":"osid-value","SSID":"ssid-value","APISID":"apisid-value","SAPISID":"sapisid-value","__Secure-1PSIDTS":"ts-value"}}`,
			want: map[string]string{
				"SID": testSID, "HSID": testHSID, "OSID": testOSID, "SSID": testSSID,
				"APISID": testAPISID, "SAPISID": testSAPISID, "__Secure-1PSIDTS": "ts-value",
			},
		},
		{
			name:  "JSON array from cookie exporter",
			input: `[{"name":"SID","value":"sid-value","domain":".google.com"},{"name":"HSID","value":"hsid-value"},{"name":"OSID","value":"osid-value"},{"name":"SSID","value":"ssid-value"},{"name":"APISID","value":"apisid-value"},{"name":"SAPISID","value":"sapisid-value"}]`,
			want:  requiredCookieMap(),
		},
		{
			name: "cURL command with Cookie header",
			input: `curl 'https://messages.google.com/web/config' \
  -H 'accept: */*' \
  -H 'cookie: SID=sid-value; HSID=hsid-value; OSID=osid-value; SSID=ssid-value; APISID=apisid-value; SAPISID=sapisid-value' \
  --compressed`,
			want: requiredCookieMap(),
		},
		{
			name:  "cURL command with double quotes and -b",
			input: `curl "https://messages.google.com/web/config" -b "SID=sid-value; HSID=hsid-value; OSID=osid-value; SSID=ssid-value; APISID=apisid-value; SAPISID=sapisid-value"`,
			want:  requiredCookieMap(),
		},
		{
			name:  "raw Cookie header line",
			input: `Cookie: SID=sid-value; HSID=hsid-value; OSID=osid-value; SSID=ssid-value; APISID=apisid-value; SAPISID=sapisid-value`,
			want:  requiredCookieMap(),
		},
		{
			name:  "raw semicolon pairs with extra cookie",
			input: `SID=sid-value; HSID=hsid-value; OSID=osid-value; SSID=ssid-value; APISID=apisid-value; SAPISID=sapisid-value; NID=extra`,
			want: map[string]string{
				"SID": testSID, "HSID": testHSID, "OSID": testOSID, "SSID": testSSID,
				"APISID": testAPISID, "SAPISID": testSAPISID, "NID": "extra",
			},
		},
		{
			name:  "value containing equals sign",
			input: `SID=sid=with=equals; HSID=hsid-value; OSID=osid-value; SSID=ssid-value; APISID=apisid-value; SAPISID=sapisid-value`,
			want: map[string]string{
				"SID": "sid=with=equals", "HSID": testHSID, "OSID": testOSID, "SSID": testSSID,
				"APISID": testAPISID, "SAPISID": testSAPISID,
			},
		},
		{
			name:  "DevTools table attributes ignored",
			input: `SID=sid-value; HSID=hsid-value; OSID=osid-value; SSID=ssid-value; APISID=apisid-value; SAPISID=sapisid-value; Path=/; Domain=.google.com; Secure; HttpOnly`,
			want:  requiredCookieMap(),
		},
		{
			name:    "missing required cookies",
			input:   `{"SID":"sid-value","HSID":"hsid-value"}`,
			wantErr: "missing required Google cookies: OSID, SSID, APISID, SAPISID",
		},
		{
			name:    "empty input",
			input:   "   ",
			wantErr: "no cookie input provided",
		},
		{
			name:    "cURL without cookie header",
			input:   `curl 'https://messages.google.com/web/config' -H 'accept: */*'`,
			wantErr: "could not find a Cookie header",
		},
		{
			name:    "invalid JSON",
			input:   `{"SID":`,
			wantErr: "cookie JSON is not valid JSON",
		},
		{
			name:    "non-string JSON value",
			input:   `{"SID":123}`,
			wantErr: `cookie "SID" must have a string value`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCookies(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseCookies_ErrorDoesNotLeakValues(t *testing.T) {
	_, err := ParseCookies(`SID=super-secret-sid; HSID=super-secret-hsid`)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "super-secret-sid")
	assert.NotContains(t, err.Error(), "super-secret-hsid")
}

func TestClient_SetCookiesAndHasCookies(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	client, err := NewClient(ClientOptions{SessionPath: sessionPath})
	require.NoError(t, err)

	assert.False(t, client.HasCookies())
	client.SetCookies(requiredCookieMap())
	assert.True(t, client.HasCookies())
	assert.Equal(t, testSID, client.AuthData().Cookies["SID"])

	// Extra cookies survive.
	client.SetCookies(map[string]string{
		"SID": testSID, "HSID": testHSID, "OSID": testOSID, "SSID": testSSID,
		"APISID": testAPISID, "SAPISID": testSAPISID, "NID": "extra",
	})
	assert.Equal(t, "extra", client.AuthData().Cookies["NID"])
	assert.True(t, strings.Contains(strings.Join(RequiredCookieNames, ","), "SID"))
}

func TestClient_HasExistingPairing(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	client, err := NewClient(ClientOptions{SessionPath: sessionPath})
	require.NoError(t, err)

	assert.False(t, client.HasExistingPairing())

	// Test nil client safety
	var nilClient *Client
	assert.False(t, nilClient.HasExistingPairing())
}
