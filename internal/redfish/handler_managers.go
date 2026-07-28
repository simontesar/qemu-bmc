package redfish

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func (s *Server) handleManagerCollection(w http.ResponseWriter, r *http.Request) {
	col := ManagerCollection{
		ODataType:    "#ManagerCollection.ManagerCollection",
		ODataID:      "/redfish/v1/Managers",
		Name:         "Manager Collection",
		MembersCount: 1,
		Members:      []ODataID{{ODataID: "/redfish/v1/Managers/1"}},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

func (s *Server) handleGetManager(w http.ResponseWriter, r *http.Request) {
	var lastResetTime string
	if t := s.getLastResetTime(); !t.IsZero() {
		lastResetTime = t.Format(time.RFC3339)
	}

	mgr := Manager{
		ODataType:       "#Manager.v1_3_0.Manager",
		ODataID:         "/redfish/v1/Managers/1",
		ODataContext:    "/redfish/v1/$metadata#Manager.Manager",
		ID:              "1",
		Name:            "QEMU BMC",
		ManagerType:     "BMC",
		Manufacturer:    s.inventory.ManagerManufacturer,
		Model:           s.inventory.ManagerModel,
		SerialNumber:    s.inventory.ManagerSerial,
		PartNumber:      s.inventory.ManagerPartNumber,
		FirmwareVersion: s.inventory.ManagerFirmwareVersion,
		LastResetTime:   lastResetTime,
		// The BMC/Manager is always running while the container is up. Clients
		// (e.g. metal-operator bmc_controller.go) read manager.PowerState and
		// manager.Status.State to decide the BMC is reachable/enabled.
		PowerState:   "On",
		Status:       ResourceStatus{State: "Enabled", Health: "OK"},
		VirtualMedia: ODataID{ODataID: "/redfish/v1/Managers/1/VirtualMedia"},
		Actions: ManagerActions{
			Reset: ResetAction{
				Target:          "/redfish/v1/Managers/1/Actions/Manager.Reset",
				AllowableValues: []string{"GracefulRestart"},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mgr)
}

func (s *Server) handleManagerReset(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "invalid request body")
		return
	}

	if req.ResetType != "GracefulRestart" {
		writeError(w, http.StatusBadRequest, "ActionParameterNotSupported", "unsupported ResetType")
		return
	}

	s.setLastResetTime(time.Now().UTC())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleVirtualMediaCollection(w http.ResponseWriter, r *http.Request) {
	col := VirtualMediaCollection{
		ODataType:    "#VirtualMediaCollection.VirtualMediaCollection",
		ODataID:      "/redfish/v1/Managers/1/VirtualMedia",
		Name:         "Virtual Media Collection",
		MembersCount: 1,
		Members:      []ODataID{{ODataID: "/redfish/v1/Managers/1/VirtualMedia/CD1"}},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

func (s *Server) handleGetVirtualMedia(w http.ResponseWriter, r *http.Request) {
	currentMedia := s.getCurrentMedia()
	vm := VirtualMedia{
		ODataType:    "#VirtualMedia.v1_2_0.VirtualMedia",
		ODataID:      "/redfish/v1/Managers/1/VirtualMedia/CD1",
		ODataContext: "/redfish/v1/$metadata#VirtualMedia.VirtualMedia",
		ID:           "CD1",
		Name:         "Virtual CD",
		MediaTypes:   []string{"CD", "DVD"},
		Image:        currentMedia,
		Inserted:     currentMedia != "",
		ConnectedVia: func() string {
			if currentMedia != "" {
				return "URI"
			}
			return "NotConnected"
		}(),
		Actions: VirtualMediaActions{
			InsertMedia: VirtualMediaAction{Target: "/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia"},
			EjectMedia:  VirtualMediaAction{Target: "/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vm)
}

func (s *Server) handleInsertMedia(w http.ResponseWriter, r *http.Request) {
	var req InsertMediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "Invalid request body")
		return
	}

	if req.Image == "" {
		writeError(w, http.StatusBadRequest, "PropertyMissing", "Image URL is required")
		return
	}

	// Track media state in BMC. QMP insertion is best-effort since
	// the URL may not be accessible until boot time.
	if err := s.machine.InsertMedia(req.Image); err != nil {
		log.Printf("VirtualMedia: QMP insert failed (non-fatal): %v", err)
	}

	s.setCurrentMedia(req.Image)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleEjectMedia(w http.ResponseWriter, r *http.Request) {
	if err := s.machine.EjectMedia(); err != nil {
		log.Printf("VirtualMedia: QMP eject failed (non-fatal): %v", err)
	}

	s.setCurrentMedia("")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
