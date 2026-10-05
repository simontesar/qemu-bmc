package redfish

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tjst-t/qemu-bmc/internal/bmc"
	"github.com/tjst-t/qemu-bmc/internal/qmp"
)

func TestBasicAuth(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "admin", "password", "")

	// A protected resource requires auth.
	t.Run("valid credentials returns 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1/Systems/1", nil)
		req.SetBasicAuth("admin", "password")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("wrong password returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1/Systems/1", nil)
		req.SetBasicAuth("admin", "wrong")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("no auth returns 401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1/Systems/1", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	// Per DSP0266 §9.2 the service root is unauthenticated so clients can
	// bootstrap the service before authenticating (e.g. gofish).
	t.Run("service root is public (no auth) returns 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestBasicAuth_SharedUserStore(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "admin", "password", "")
	users := bmc.NewState("admin", "password")
	srv.SetUserStore(users)
	_, err := users.CreateAccount("operator", "secret", 3, true)
	require.NoError(t, err)
	_, err = users.CreateAccount("disabled", "secret", 2, false)
	require.NoError(t, err)

	tests := []struct {
		name, user, pass string
		code             int
	}{
		{"store account authenticates", "operator", "secret", http.StatusOK},
		{"store account wrong password", "operator", "wrong", http.StatusUnauthorized},
		{"disabled account rejected", "disabled", "secret", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/redfish/v1/Systems/1", nil)
			req.SetBasicAuth(tt.user, tt.pass)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			assert.Equal(t, tt.code, w.Code)
		})
	}
}

func TestTrailingSlash(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	t.Run("without trailing slash returns 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1/Systems", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("with trailing slash returns 200", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1/Systems/", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}
