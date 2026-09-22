package redfish

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tjst-t/qemu-bmc/internal/qmp"
)

func TestBootOptionCollection(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "", "", "")

	req := httptest.NewRequest("GET", "/redfish/v1/Systems/1/BootOptions", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var col BootOptionCollection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &col))

	assert.Equal(t, 3, col.MembersCount)
	assert.ElementsMatch(t, []ODataID{
		{ODataID: "/redfish/v1/Systems/1/BootOptions/Pxe"},
		{ODataID: "/redfish/v1/Systems/1/BootOptions/Hdd"},
		{ODataID: "/redfish/v1/Systems/1/BootOptions/Cd"},
	}, col.Members)
}

func TestGetBootOption(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "", "", "")

	t.Run("known reference returns its details", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1/Systems/1/BootOptions/Hdd", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var bo BootOption
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bo))
		assert.Equal(t, "Hdd", bo.ID)
		assert.Equal(t, "Hdd", bo.BootOptionReference)
		assert.True(t, bo.BootOptionEnabled)
	})

	t.Run("unknown reference 404s", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1/Systems/1/BootOptions/Nvme0", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestGetSystem_BootOptionsLink(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "", "", "")
	sys := getSystem(t, srv)
	assert.Equal(t, "/redfish/v1/Systems/1/BootOptions", sys.BootOptions.ODataID)
}
