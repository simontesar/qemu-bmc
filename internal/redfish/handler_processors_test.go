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

func TestGetProcessorCollection(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	req := httptest.NewRequest("GET", "/redfish/v1/Systems/1/Processors", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var col ProcessorCollection
	err := json.Unmarshal(w.Body.Bytes(), &col)
	require.NoError(t, err)
	assert.Equal(t, 1, col.MembersCount)
	assert.Equal(t, "/redfish/v1/Systems/1/Processors/1", col.Members[0].ODataID)
}

func TestGetProcessor(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")
	srv.SetInventory(testInventory)

	req := httptest.NewRequest("GET", "/redfish/v1/Systems/1/Processors/1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var proc Processor
	err := json.Unmarshal(w.Body.Bytes(), &proc)
	require.NoError(t, err)
	assert.Equal(t, "CPU", proc.ProcessorType)
	assert.Equal(t, testInventory.CPUModel, proc.Model)
	require.NotNil(t, proc.TotalCores)
	require.NotNil(t, proc.TotalThreads)
	assert.Equal(t, testInventory.CPUCount, *proc.TotalCores)
	assert.Equal(t, testInventory.CPUCount, *proc.TotalThreads)
}

func TestGetProcessor_NotFound(t *testing.T) {
	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")

	req := httptest.NewRequest("GET", "/redfish/v1/Systems/1/Processors/2", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
