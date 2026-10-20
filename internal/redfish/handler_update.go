package redfish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/tjst-t/qemu-bmc/internal/firmware"
)

const (
	updateServicePath     = "/redfish/v1/UpdateService"
	firmwareInventoryPath = updateServicePath + "/FirmwareInventory"
	simpleUpdatePath      = updateServicePath + "/Actions/UpdateService.SimpleUpdate"
	taskServicePath       = "/redfish/v1/TaskService"
	tasksPath             = taskServicePath + "/Tasks"

	installedPrefix = "Installed-"
	stagedPrefix    = "Staged-"

	// Defaults of the updater NewServer installs, overridden from main.
	defaultFirmwareDownloadTimeout = 30 * time.Second
	defaultFirmwareUpdateDuration  = 20 * time.Second
)

var transferProtocols = []string{"HTTP", "HTTPS"}

// firmwareComponent describes a component's FirmwareInventory entries. Ids
// follow Dell iDRAC's "<Installed|Staged>-<FQDD>" shape: metal-operator's Dell
// client detects pending updates by matching "BIOS"/"iDRAC" in the Id and
// checking Staged.
type firmwareComponent struct {
	fqdd        string
	name        string
	relatedItem string
	// activation names what activates a staged image, for task messages.
	activation string
}

var firmwareComponents = map[firmware.Component]firmwareComponent{
	firmware.ComponentBIOS: {fqdd: "BIOS.Setup.1-1", name: "BIOS", relatedItem: "/redfish/v1/Systems/1", activation: "system reset"},
	firmware.ComponentBMC:  {fqdd: "iDRAC.Embedded.1-1", name: "BMC Firmware", relatedItem: "/redfish/v1/Managers/1", activation: "BMC reset"},
}

var firmwareComponentOrder = []firmware.Component{firmware.ComponentBIOS, firmware.ComponentBMC}

func firmwareInventoryItemPath(id string) string {
	return firmwareInventoryPath + "/" + id
}

func taskPath(id string) string {
	return tasksPath + "/" + id
}

func (s *Server) handleGetUpdateService(w http.ResponseWriter, r *http.Request) {
	svc := UpdateService{
		ODataType:         "#UpdateService.v1_11_0.UpdateService",
		ODataID:           updateServicePath,
		ID:                "UpdateService",
		Name:              "Update Service",
		ServiceEnabled:    true,
		FirmwareInventory: ODataID{ODataID: firmwareInventoryPath},
		Actions: UpdateServiceActions{
			SimpleUpdate: SimpleUpdateAction{
				Target:                    simpleUpdatePath,
				TransferProtocolAllowable: transferProtocols,
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(svc)
}

func (s *Server) handleFirmwareInventoryCollection(w http.ResponseWriter, r *http.Request) {
	var members []ODataID
	for _, c := range firmwareComponentOrder {
		members = append(members, ODataID{ODataID: firmwareInventoryItemPath(installedPrefix + firmwareComponents[c].fqdd)})
		if _, ok := s.getStagedFirmware(c); ok {
			members = append(members, ODataID{ODataID: firmwareInventoryItemPath(stagedPrefix + firmwareComponents[c].fqdd)})
		}
	}
	col := SoftwareInventoryCollection{
		ODataType:    "#SoftwareInventoryCollection.SoftwareInventoryCollection",
		ODataID:      firmwareInventoryPath,
		Name:         "Firmware Inventory Collection",
		MembersCount: len(members),
		Members:      members,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

func (s *Server) handleGetFirmwareInventory(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	for _, c := range firmwareComponentOrder {
		comp := firmwareComponents[c]
		var version string
		var staged bool
		switch id {
		case installedPrefix + comp.fqdd:
			version = s.getFirmwareVersion(c)
		case stagedPrefix + comp.fqdd:
			v, ok := s.getStagedFirmware(c)
			if !ok {
				continue
			}
			version, staged = v, true
		default:
			continue
		}
		item := SoftwareInventory{
			ODataType:   "#SoftwareInventory.v1_3_0.SoftwareInventory",
			ODataID:     firmwareInventoryItemPath(id),
			ID:          id,
			Name:        comp.name,
			Version:     version,
			Updateable:  true,
			Staged:      staged,
			RelatedItem: []ODataID{{ODataID: comp.relatedItem}},
			Status:      ResourceStatus{State: "Enabled", Health: "OK"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(item)
		return
	}
	writeError(w, http.StatusNotFound, "ResourceNotFound", "firmware inventory item not found: "+id)
}

// handleSimpleUpdate accepts a firmware update and runs it as a Task. The image
// is downloaded and validated asynchronously; once "flashed" it is staged and
// activated on the next system reset (BIOS) or BMC reset (BMC), like a real
// BMC. Clients poll the Task returned via the Location header.
func (s *Server) handleSimpleUpdate(w http.ResponseWriter, r *http.Request) {
	var req SimpleUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "invalid request body")
		return
	}
	if req.ImageURI == "" {
		writeError(w, http.StatusBadRequest, "PropertyMissing", "ImageURI is required")
		return
	}
	imageURI, err := resolveImageURI(req.ImageURI, req.TransferProtocol)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ActionParameterNotSupported", err.Error())
		return
	}
	switch req.OperationApplyTime {
	case "", "Immediate", "OnReset":
	default:
		writeError(w, http.StatusBadRequest, "ActionParameterNotSupported",
			"unsupported @Redfish.OperationApplyTime: "+req.OperationApplyTime)
		return
	}
	targets, err := resolveTargets(req.Targets)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ActionParameterValueNotInList", err.Error())
		return
	}

	task := s.createTask("Firmware Update: " + imageURI)
	s.debugf("UpdateService: SimpleUpdate image=%s targets=%v applyTime=%q force=%t -> task %s",
		imageURI, targets, req.OperationApplyTime, req.ForceUpdate, task.ID)
	go s.runFirmwareUpdate(task.ID, firmware.Request{
		ImageURI: imageURI,
		Username: req.Username,
		Password: req.Password,
		Targets:  targets,
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", task.ODataID)
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(task)
}

// resolveImageURI validates the ImageURI against TransferProtocol. A URI
// without a scheme takes it from TransferProtocol.
func resolveImageURI(imageURI, protocol string) (string, error) {
	protocol = strings.ToUpper(protocol)
	if protocol != "" && !slices.Contains(transferProtocols, protocol) {
		return "", fmt.Errorf("unsupported TransferProtocol %q, supported: %v", protocol, transferProtocols)
	}
	if !strings.Contains(imageURI, "://") {
		if protocol == "" {
			return "", fmt.Errorf("ImageURI %q has no scheme and no TransferProtocol is given", imageURI)
		}
		imageURI = strings.ToLower(protocol) + "://" + imageURI
	}
	u, err := url.Parse(imageURI)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid ImageURI %q", imageURI)
	}
	scheme := strings.ToUpper(u.Scheme)
	if !slices.Contains(transferProtocols, scheme) {
		return "", fmt.Errorf("unsupported ImageURI scheme %q, supported: %v", u.Scheme, transferProtocols)
	}
	if protocol != "" && protocol != scheme {
		return "", fmt.Errorf("TransferProtocol %q does not match ImageURI scheme %q", protocol, u.Scheme)
	}
	return imageURI, nil
}

// resolveTargets maps SimpleUpdate Targets to components. A target is either a
// component's installed FirmwareInventory entry or the resource it applies to.
func resolveTargets(targets []string) ([]firmware.Component, error) {
	var out []firmware.Component
	for _, target := range targets {
		target = strings.TrimSuffix(target, "/")
		found := false
		for _, c := range firmwareComponentOrder {
			comp := firmwareComponents[c]
			if target == firmwareInventoryItemPath(installedPrefix+comp.fqdd) || target == comp.relatedItem {
				out = append(out, c)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unsupported target %q", target)
		}
	}
	return out, nil
}

// runFirmwareUpdate drives a SimpleUpdate task to completion and stages the
// installed package for activation.
func (s *Server) runFirmwareUpdate(taskID string, req firmware.Request) {
	s.updateTask(taskID, func(t *Task) { t.TaskState = "Running" })
	pkg, err := s.firmwareUpdater.Run(context.Background(), req, func(percent int) {
		s.updateTask(taskID, func(t *Task) { t.PercentComplete = percent })
	})
	end := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		s.debugf("UpdateService: task %s failed: %v", taskID, err)
		s.updateTask(taskID, func(t *Task) {
			t.TaskState = "Exception"
			t.TaskStatus = "Critical"
			t.EndTime = end
			t.Messages = append(t.Messages, Message{
				MessageID:       "TaskEvent.1.0.TaskAborted",
				Message:         err.Error(),
				Severity:        "Critical",
				MessageSeverity: "Critical",
				MessageArgs:     []string{taskID},
			})
		})
		return
	}

	s.stageFirmware(pkg.Component, pkg.Version)
	comp := firmwareComponents[pkg.Component]
	s.debugf("UpdateService: task %s staged %s %s, activates on next %s", taskID, pkg.Component, pkg.Version, comp.activation)
	s.updateTask(taskID, func(t *Task) {
		t.TaskState = "Completed"
		t.TaskStatus = "OK"
		t.PercentComplete = 100
		t.EndTime = end
		t.Messages = append(t.Messages, Message{
			MessageID:       "Update.1.0.AwaitToActivate",
			Message:         fmt.Sprintf("%s version %s is staged and activates on the next %s.", comp.name, pkg.Version, comp.activation),
			Severity:        "OK",
			MessageSeverity: "OK",
			MessageArgs:     []string{req.ImageURI, comp.activation},
		})
	})
}

func (s *Server) handleGetTaskService(w http.ResponseWriter, r *http.Request) {
	svc := TaskService{
		ODataType:      "#TaskService.v1_2_0.TaskService",
		ODataID:        taskServicePath,
		ID:             "TaskService",
		Name:           "Task Service",
		ServiceEnabled: true,
		Tasks:          ODataID{ODataID: tasksPath},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(svc)
}

func (s *Server) handleTaskCollection(w http.ResponseWriter, r *http.Request) {
	ids := s.listTaskIDs()
	members := make([]ODataID, 0, len(ids))
	for _, id := range ids {
		members = append(members, ODataID{ODataID: taskPath(id)})
	}
	col := TaskCollection{
		ODataType:    "#TaskCollection.TaskCollection",
		ODataID:      tasksPath,
		Name:         "Task Collection",
		MembersCount: len(members),
		Members:      members,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	task, ok := s.getTask(id)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "task not found: "+id)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// createTask registers a new task in state New. Ids mimic Dell iDRAC job ids.
func (s *Server) createTask(name string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTaskID++
	id := fmt.Sprintf("JID_%012d", s.nextTaskID)
	task := Task{
		ODataType:   "#Task.v1_4_3.Task",
		ODataID:     taskPath(id),
		ID:          id,
		Name:        name,
		TaskState:   "New",
		TaskStatus:  "OK",
		StartTime:   time.Now().UTC().Format(time.RFC3339),
		TaskMonitor: taskPath(id),
		Messages:    []Message{},
	}
	s.tasks[id] = task
	return task
}

func (s *Server) updateTask(id string, fn func(*Task)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return
	}
	task.Messages = append([]Message(nil), task.Messages...)
	fn(&task)
	s.tasks[id] = task
}

func (s *Server) getTask(id string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	return task, ok
}

// listTaskIDs returns task ids in creation order (ids are zero-padded).
func (s *Server) listTaskIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.tasks))
	for id := range s.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
