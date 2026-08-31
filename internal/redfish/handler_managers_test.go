package redfish

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tjst-t/qemu-bmc/internal/qmp"
)

var testInventory = Inventory{
	SystemUUID:             "uuid-1",
	SystemManufacturer:     "QEMU",
	SystemModel:            "Standard PC (Q35 + ICH9, 2009)",
	SystemSerial:           "serial-1",
	SystemSKU:              "sku-1",
	SystemBiosVersion:      "1.0.0",
	ManagerModel:           "Standard PC (Q35 + ICH9, 2009)",
	ManagerFirmwareVersion: "1.0.0",
	ManagerManufacturer:    "QEMU",
	ManagerSerial:          "serial-1",
	ManagerPartNumber:      "part-1",
	CPUModel:               "QEMU Virtual CPU",
	CPUCount:               4,
	MemoryMiB:              2048,
}

func TestGetManagerCollection(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	req := httptest.NewRequest("GET", "/redfish/v1/Managers", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var col ManagerCollection
	err := json.Unmarshal(w.Body.Bytes(), &col)
	require.NoError(t, err)
	assert.Equal(t, 1, col.MembersCount)
	assert.Equal(t, "/redfish/v1/Managers/1", col.Members[0].ODataID)
}

func TestGetManager(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	req := httptest.NewRequest("GET", "/redfish/v1/Managers/1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var mgr Manager
	err := json.Unmarshal(w.Body.Bytes(), &mgr)
	require.NoError(t, err)
	assert.Equal(t, "#Manager.v1_3_0.Manager", mgr.ODataType)
	assert.Equal(t, "BMC", mgr.ManagerType)
}

func TestGetManager_Inventory(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")
	srv.SetInventory(testInventory)

	req := httptest.NewRequest("GET", "/redfish/v1/Managers/1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var mgr Manager
	err := json.Unmarshal(w.Body.Bytes(), &mgr)
	require.NoError(t, err)
	assert.Equal(t, "Standard PC (Q35 + ICH9, 2009)", mgr.Model)
	assert.Equal(t, "1.0.0", mgr.FirmwareVersion)
	assert.Equal(t, "QEMU", mgr.Manufacturer)
	assert.Equal(t, "serial-1", mgr.SerialNumber)
	assert.Equal(t, "part-1", mgr.PartNumber)
	assert.Empty(t, mgr.LastResetTime)
	assert.Equal(t, "/redfish/v1/Managers/1/Actions/Manager.Reset", mgr.Actions.Reset.Target)
	assert.Contains(t, mgr.Actions.Reset.AllowableValues, "GracefulRestart")
}

func TestGetManager_ExposesDellAttributesLink(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")
	srv.SetDellBMCAttributes(true)

	req := httptest.NewRequest("GET", "/redfish/v1/Managers/1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var mgr Manager
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mgr))
	require.NotNil(t, mgr.Links)
	require.Len(t, mgr.Links.Oem.Dell.DellAttributes, 1)
	assert.Equal(t, "/redfish/v1/Managers/1/Attributes", mgr.Links.Oem.Dell.DellAttributes[0].ODataID)
	assert.Equal(t, 1, mgr.Links.Oem.Dell.DellAttributesCount)
}

func TestGetManagerAttributes(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")
	srv.SetDellBMCAttributes(true)

	req := httptest.NewRequest("GET", "/redfish/v1/Managers/1/Attributes", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("ETag"))
	var attrs DellManagerAttributes
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &attrs))
	assert.Equal(t, "Attributes", attrs.ID)
	assert.Equal(t, managerAttributeRegistryID, attrs.AttributeRegistry)
	if _, ok := attrs.Attributes["EmailAlert.1.Address"]; !ok {
		t.Fatalf("expected EmailAlert.1.Address in manager attributes, got %v", attrs.Attributes)
	}
	require.NotNil(t, attrs.RedfishSettings)
	assert.Equal(t, "/redfish/v1/Managers/1/Attributes/Settings", attrs.RedfishSettings.SettingsObject.ODataID)
}

func TestGetManagerAttributesSettings_AlwaysEmpty(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")
	srv.SetDellBMCAttributes(true)

	req := httptest.NewRequest("GET", "/redfish/v1/Managers/1/Attributes/Settings", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var attrs DellManagerAttributes
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &attrs))
	assert.Equal(t, "Settings", attrs.ID)
	assert.Empty(t, attrs.Attributes)
}

func TestPatchManagerAttributesSettings_AppliesImmediately(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")
	srv.SetDellBMCAttributes(true)

	body := `{"Attributes": {"EmailAlert.1.Address": "alerts@example.com"}, "@Redfish.SettingsApplyTime": {"ApplyTime": "Immediate"}}`
	req := httptest.NewRequest("PATCH", "/redfish/v1/Managers/1/Attributes/Settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", `"manager-attributes-settings"`)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	getReq := httptest.NewRequest("GET", "/redfish/v1/Managers/1/Attributes", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, getReq)
	var attrs DellManagerAttributes
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &attrs))
	assert.Equal(t, "alerts@example.com", attrs.Attributes["EmailAlert.1.Address"])

	// Pending set stays empty (immediate apply).
	getSettings := httptest.NewRequest("GET", "/redfish/v1/Managers/1/Attributes/Settings", nil)
	w3 := httptest.NewRecorder()
	srv.ServeHTTP(w3, getSettings)
	var pending DellManagerAttributes
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &pending))
	assert.Empty(t, pending.Attributes)
}

func TestPatchManagerAttributesSettings_NoAttributes(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")
	srv.SetDellBMCAttributes(true)

	req := httptest.NewRequest("PATCH", "/redfish/v1/Managers/1/Attributes/Settings", strings.NewReader(`{"Attributes": {}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestManagerAttributes_DisabledByDefault verifies the Dell iDRAC-style attribute
// surface is absent unless SetDellBMCAttributes(true) is called.
func TestManagerAttributes_DisabledByDefault(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "", "", "")

	t.Run("Manager has no Links block", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/redfish/v1/Managers/1", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		var mgr Manager
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mgr))
		assert.Nil(t, mgr.Links)
		assert.NotContains(t, w.Body.String(), `"Links"`)
	})

	for _, tc := range []struct {
		method, path string
		body         string
	}{
		{"GET", "/redfish/v1/Managers/1/Attributes", ""},
		{"GET", "/redfish/v1/Managers/1/Attributes/Settings", ""},
		{"PATCH", "/redfish/v1/Managers/1/Attributes/Settings", `{"Attributes":{"EmailAlert.1.Address":"x@y.z"}}`},
	} {
		t.Run(tc.method+" "+tc.path+" is 404", func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			assert.Equal(t, http.StatusNotFound, w.Code)
		})
	}
}

func TestHandleManagerReset(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	body := `{"ResetType": "GracefulRestart"}`
	req := httptest.NewRequest("POST", "/redfish/v1/Managers/1/Actions/Manager.Reset", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	getReq := httptest.NewRequest("GET", "/redfish/v1/Managers/1", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, getReq)

	var mgr Manager
	err := json.Unmarshal(w2.Body.Bytes(), &mgr)
	require.NoError(t, err)
	assert.NotEmpty(t, mgr.LastResetTime)
}

func TestHandleManagerReset_UnsupportedType(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	body := `{"ResetType": "ForceRestart"}`
	req := httptest.NewRequest("POST", "/redfish/v1/Managers/1/Actions/Manager.Reset", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetVirtualMediaCollection(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	req := httptest.NewRequest("GET", "/redfish/v1/Managers/1/VirtualMedia", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var col VirtualMediaCollection
	err := json.Unmarshal(w.Body.Bytes(), &col)
	require.NoError(t, err)
	assert.Equal(t, 1, col.MembersCount)
}

func TestGetVirtualMedia_NotInserted(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	req := httptest.NewRequest("GET", "/redfish/v1/Managers/1/VirtualMedia/CD1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var vm VirtualMedia
	err := json.Unmarshal(w.Body.Bytes(), &vm)
	require.NoError(t, err)
	assert.False(t, vm.Inserted)
	assert.Empty(t, vm.Image)
}

func TestInsertVirtualMedia(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	body := `{"Image": "http://example.com/boot.iso", "Inserted": true}`
	req := httptest.NewRequest("POST",
		"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "http://example.com/boot.iso", mock.LastInsertedMedia())
}

func TestInsertVirtualMedia_EmptyImage(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	body := `{"Image": "", "Inserted": true}`
	req := httptest.NewRequest("POST",
		"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestEjectVirtualMedia(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	mock.lastMedia = "http://example.com/boot.iso"
	srv := NewServer(mock, "", "", "")

	req := httptest.NewRequest("POST",
		"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia",
		nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestVirtualMedia_InsertThenGet(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	// Insert
	body := `{"Image": "http://example.com/boot.iso", "Inserted": true}`
	insertReq := httptest.NewRequest("POST",
		"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		strings.NewReader(body))
	insertReq.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.ServeHTTP(w1, insertReq)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Get - should show inserted
	getReq := httptest.NewRequest("GET", "/redfish/v1/Managers/1/VirtualMedia/CD1", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, getReq)

	var vm VirtualMedia
	json.Unmarshal(w2.Body.Bytes(), &vm)
	assert.True(t, vm.Inserted)
	assert.Equal(t, "http://example.com/boot.iso", vm.Image)
}
