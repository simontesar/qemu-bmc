package redfish

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tjst-t/qemu-bmc/internal/firmware"
	"github.com/tjst-t/qemu-bmc/internal/qmp"
)

// dellSimpleUpdate mirrors the body metal-operator's Dell client POSTs for
// BIOSVersion/BMCVersion: no Targets, Immediate apply time.
const dellSimpleUpdate = `{
	"ImageURI": "%s",
	"TransferProtocol": "HTTP",
	"@Redfish.OperationApplyTime": "Immediate"
}`

func newUpdateServer(t *testing.T) (*Server, *mockMachine, string) {
	t.Helper()
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bios-1.1.0.json":
			w.Write([]byte(`{"Component":"BIOS","Version":"1.1.0"}`))
		case "/bmc-1.1.0.json":
			w.Write([]byte(`{"Component":"BMC","Version":"1.1.0"}`))
		case "/invalid.json":
			w.Write([]byte(`{"Component":"NIC","Version":"1.1.0"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(repo.Close)

	mock := newMockMachine(qmp.StatusRunning)
	srv := NewServer(mock, "", "", "")
	srv.SetInventory(testInventory)
	srv.SetFirmwareUpdater(firmware.NewUpdater(time.Second, 0, false))
	return srv, mock, repo.URL
}

func postSimpleUpdate(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doAccountRequest(srv, "POST", "/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate", body, "", "")
}

// waitForTask polls the task until it leaves New/Running.
func waitForTask(t *testing.T, srv *Server, uri string) Task {
	t.Helper()
	var task Task
	require.Eventually(t, func() bool {
		w := doAccountRequest(srv, "GET", uri, "", "", "")
		require.Equal(t, http.StatusOK, w.Code)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &task))
		return task.TaskState != "New" && task.TaskState != "Running"
	}, 2*time.Second, 10*time.Millisecond)
	return task
}

func getFirmwareInventory(t *testing.T, srv *Server) map[string]SoftwareInventory {
	t.Helper()
	w := doAccountRequest(srv, "GET", "/redfish/v1/UpdateService/FirmwareInventory", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var col SoftwareInventoryCollection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &col))
	require.Len(t, col.Members, col.MembersCount)

	items := map[string]SoftwareInventory{}
	for _, m := range col.Members {
		w := doAccountRequest(srv, "GET", m.ODataID, "", "", "")
		require.Equal(t, http.StatusOK, w.Code)
		var item SoftwareInventory
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &item))
		items[item.ID] = item
	}
	return items
}

func TestServiceRoot_LinksUpdateAndTaskService(t *testing.T) {
	srv, _, _ := newUpdateServer(t)
	w := doAccountRequest(srv, "GET", "/redfish/v1", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var root ServiceRoot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &root))
	assert.Equal(t, "/redfish/v1/UpdateService", root.UpdateService.ODataID)
	assert.Equal(t, "/redfish/v1/TaskService", root.Tasks.ODataID)
}

func TestGetUpdateService(t *testing.T) {
	srv, _, _ := newUpdateServer(t)
	w := doAccountRequest(srv, "GET", "/redfish/v1/UpdateService", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var svc UpdateService
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &svc))
	assert.Equal(t, "#UpdateService.v1_11_0.UpdateService", svc.ODataType)
	assert.True(t, svc.ServiceEnabled)
	assert.Equal(t, "/redfish/v1/UpdateService/FirmwareInventory", svc.FirmwareInventory.ODataID)
	assert.Equal(t, "/redfish/v1/UpdateService/Actions/UpdateService.SimpleUpdate", svc.Actions.SimpleUpdate.Target)
	assert.Equal(t, []string{"HTTP", "HTTPS"}, svc.Actions.SimpleUpdate.TransferProtocolAllowable)
}

func TestFirmwareInventory_Installed(t *testing.T) {
	srv, _, _ := newUpdateServer(t)
	items := getFirmwareInventory(t, srv)
	require.Len(t, items, 2)

	bios := items["Installed-BIOS.Setup.1-1"]
	assert.Equal(t, "1.0.0", bios.Version)
	assert.False(t, bios.Staged)
	assert.True(t, bios.Updateable)
	assert.Equal(t, []ODataID{{ODataID: "/redfish/v1/Systems/1"}}, bios.RelatedItem)

	bmc := items["Installed-iDRAC.Embedded.1-1"]
	assert.Equal(t, "1.0.0", bmc.Version)
	assert.False(t, bmc.Staged)
	assert.Equal(t, []ODataID{{ODataID: "/redfish/v1/Managers/1"}}, bmc.RelatedItem)

	w := doAccountRequest(srv, "GET", "/redfish/v1/UpdateService/FirmwareInventory/Staged-BIOS.Setup.1-1", "", "", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSimpleUpdate_BIOS_ActivatesOnSystemReset(t *testing.T) {
	srv, mock, repo := newUpdateServer(t)

	w := postSimpleUpdate(t, srv, fmt.Sprintf(dellSimpleUpdate, repo+"/bios-1.1.0.json"))
	require.Equal(t, http.StatusAccepted, w.Code)
	location := w.Header().Get("Location")
	assert.Regexp(t, `^/redfish/v1/TaskService/Tasks/JID_\d{12}$`, location)
	var accepted Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	assert.Equal(t, location, accepted.ODataID)

	task := waitForTask(t, srv, location)
	assert.Equal(t, "Completed", task.TaskState)
	assert.Equal(t, "OK", task.TaskStatus)
	assert.Equal(t, 100, task.PercentComplete)
	assert.NotEmpty(t, task.EndTime)
	require.Len(t, task.Messages, 1)
	assert.Equal(t, "Update.1.0.AwaitToActivate", task.Messages[0].MessageID)

	// Staged until the next power-on reset: still 1.0.0, a staged entry
	// reports the pending image (what metal-operator's pending check reads).
	assert.Equal(t, "1.0.0", getSystem(t, srv).BiosVersion)
	items := getFirmwareInventory(t, srv)
	staged := items["Staged-BIOS.Setup.1-1"]
	assert.True(t, staged.Staged)
	assert.Equal(t, "1.1.0", staged.Version)

	// GracefulShutdown does not activate it, a restart does.
	w = doAccountRequest(srv, "POST", "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", `{"ResetType":"GracefulShutdown"}`, "", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "1.0.0", getSystem(t, srv).BiosVersion)

	w = doAccountRequest(srv, "POST", "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", `{"ResetType":"GracefulRestart"}`, "", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Contains(t, mock.calls, "GracefulRestart")
	assert.Equal(t, "1.1.0", getSystem(t, srv).BiosVersion)

	items = getFirmwareInventory(t, srv)
	assert.Len(t, items, 2)
	assert.Equal(t, "1.1.0", items["Installed-BIOS.Setup.1-1"].Version)
	assert.Equal(t, "1.0.0", items["Installed-iDRAC.Embedded.1-1"].Version)
}

func TestSimpleUpdate_BMC_ActivatesOnManagerReset(t *testing.T) {
	srv, _, repo := newUpdateServer(t)

	w := postSimpleUpdate(t, srv, fmt.Sprintf(dellSimpleUpdate, repo+"/bmc-1.1.0.json"))
	require.Equal(t, http.StatusAccepted, w.Code)
	task := waitForTask(t, srv, w.Header().Get("Location"))
	require.Equal(t, "Completed", task.TaskState)

	assert.Equal(t, "1.0.0", getManager(t, srv).FirmwareVersion)
	assert.True(t, getFirmwareInventory(t, srv)["Staged-iDRAC.Embedded.1-1"].Staged)

	// A system reset does not activate BMC firmware.
	w = doAccountRequest(srv, "POST", "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", `{"ResetType":"GracefulRestart"}`, "", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "1.0.0", getManager(t, srv).FirmwareVersion)

	w = doAccountRequest(srv, "POST", "/redfish/v1/Managers/1/Actions/Manager.Reset", `{"ResetType":"GracefulRestart"}`, "", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "1.1.0", getManager(t, srv).FirmwareVersion)
	assert.Equal(t, "1.0.0", getSystem(t, srv).BiosVersion)
	assert.NotContains(t, getFirmwareInventory(t, srv), "Staged-iDRAC.Embedded.1-1")
}

func TestSimpleUpdate_Targets(t *testing.T) {
	srv, _, repo := newUpdateServer(t)

	// The installed inventory entry and the related resource are both accepted.
	for _, target := range []string{
		"/redfish/v1/UpdateService/FirmwareInventory/Installed-BIOS.Setup.1-1",
		"/redfish/v1/Systems/1",
	} {
		w := postSimpleUpdate(t, srv, fmt.Sprintf(`{"ImageURI":"%s","Targets":["%s"]}`, repo+"/bios-1.1.0.json", target))
		require.Equal(t, http.StatusAccepted, w.Code, target)
		assert.Equal(t, "Completed", waitForTask(t, srv, w.Header().Get("Location")).TaskState, target)
	}

	// A BIOS package aimed at the BMC fails the task.
	w := postSimpleUpdate(t, srv, fmt.Sprintf(`{"ImageURI":"%s","Targets":["/redfish/v1/Managers/1"]}`, repo+"/bios-1.1.0.json"))
	require.Equal(t, http.StatusAccepted, w.Code)
	task := waitForTask(t, srv, w.Header().Get("Location"))
	assert.Equal(t, "Exception", task.TaskState)
	assert.Equal(t, "Critical", task.TaskStatus)
}

func TestSimpleUpdate_TaskFailures(t *testing.T) {
	srv, _, repo := newUpdateServer(t)

	for name, image := range map[string]string{
		"download fails":  repo + "/missing.json",
		"invalid package": repo + "/invalid.json",
	} {
		t.Run(name, func(t *testing.T) {
			w := postSimpleUpdate(t, srv, fmt.Sprintf(dellSimpleUpdate, image))
			require.Equal(t, http.StatusAccepted, w.Code)
			task := waitForTask(t, srv, w.Header().Get("Location"))
			assert.Equal(t, "Exception", task.TaskState)
			assert.Equal(t, "Critical", task.TaskStatus)
			require.Len(t, task.Messages, 1)
			assert.Equal(t, "TaskEvent.1.0.TaskAborted", task.Messages[0].MessageID)
		})
	}
	assert.Len(t, getFirmwareInventory(t, srv), 2, "failed updates stage nothing")
}

func TestSimpleUpdate_RejectsInvalidRequests(t *testing.T) {
	srv, _, repo := newUpdateServer(t)

	for name, body := range map[string]string{
		"malformed":             `{`,
		"missing ImageURI":      `{"TransferProtocol":"HTTP"}`,
		"unsupported protocol":  fmt.Sprintf(`{"ImageURI":"%s","TransferProtocol":"NFS"}`, repo+"/bios-1.1.0.json"),
		"unsupported scheme":    `{"ImageURI":"ftp://repo/bios-1.1.0.json"}`,
		"protocol mismatch":     fmt.Sprintf(`{"ImageURI":"%s","TransferProtocol":"HTTPS"}`, repo+"/bios-1.1.0.json"),
		"no scheme or protocol": `{"ImageURI":"repo/bios-1.1.0.json"}`,
		"unsupported applytime": fmt.Sprintf(`{"ImageURI":"%s","@Redfish.OperationApplyTime":"AtMaintenanceWindowStart"}`, repo+"/bios-1.1.0.json"),
		"unknown target":        fmt.Sprintf(`{"ImageURI":"%s","Targets":["/redfish/v1/Chassis/1"]}`, repo+"/bios-1.1.0.json"),
	} {
		t.Run(name, func(t *testing.T) {
			w := postSimpleUpdate(t, srv, body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}

	w := doAccountRequest(srv, "GET", "/redfish/v1/TaskService/Tasks", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var col TaskCollection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &col))
	assert.Equal(t, 0, col.MembersCount, "rejected requests create no task")
}

func TestSimpleUpdate_ImageURIWithoutScheme(t *testing.T) {
	srv, _, repo := newUpdateServer(t)
	hostPath := repo[len("http://"):] + "/bios-1.1.0.json"

	w := postSimpleUpdate(t, srv, fmt.Sprintf(`{"ImageURI":"%s","TransferProtocol":"HTTP"}`, hostPath))
	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "Completed", waitForTask(t, srv, w.Header().Get("Location")).TaskState)
}

func TestTaskService(t *testing.T) {
	srv, _, repo := newUpdateServer(t)

	w := doAccountRequest(srv, "GET", "/redfish/v1/TaskService", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var svc TaskService
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &svc))
	assert.Equal(t, "/redfish/v1/TaskService/Tasks", svc.Tasks.ODataID)

	var locations []string
	for range 2 {
		w := postSimpleUpdate(t, srv, fmt.Sprintf(dellSimpleUpdate, repo+"/bios-1.1.0.json"))
		require.Equal(t, http.StatusAccepted, w.Code)
		locations = append(locations, w.Header().Get("Location"))
		waitForTask(t, srv, locations[len(locations)-1])
	}

	w = doAccountRequest(srv, "GET", "/redfish/v1/TaskService/Tasks", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var col TaskCollection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &col))
	assert.Equal(t, []ODataID{{ODataID: locations[0]}, {ODataID: locations[1]}}, col.Members)

	w = doAccountRequest(srv, "GET", "/redfish/v1/TaskService/Tasks/JID_999999999999", "", "", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateService_RequiresAuth(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")

	for _, path := range []string{"/redfish/v1/UpdateService", "/redfish/v1/TaskService/Tasks"} {
		w := doAccountRequest(srv, "GET", path, "", "", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code, path)
		w = doAccountRequest(srv, "GET", path, "", "admin", "password")
		assert.Equal(t, http.StatusOK, w.Code, path)
	}
}

func getManager(t *testing.T, srv *Server) Manager {
	t.Helper()
	w := doAccountRequest(srv, "GET", "/redfish/v1/Managers/1", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var mgr Manager
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mgr))
	return mgr
}
