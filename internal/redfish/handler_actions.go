package redfish

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/tjst-t/qemu-bmc/internal/firmware"
)

var validResetTypes = map[string]bool{
	"On":               true,
	"ForceOn":          true,
	"ForceOff":         true,
	"GracefulShutdown": true,
	"ForceRestart":     true,
	"GracefulRestart":  true,
	"PowerCycle":       true,
}

// powerOnResetTypes are the ResetTypes that leave the VM powered on once
// Reset() returns, i.e. these after which pending BIOS settings and staged BIOS
// firmware should be applied.
var powerOnResetTypes = map[string]bool{
	"On":              true,
	"ForceOn":         true,
	"ForceRestart":    true,
	"GracefulRestart": true,
	"PowerCycle":      true,
}

func (s *Server) handleResetAction(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req ResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "invalid request body")
		return
	}

	if !validResetTypes[req.ResetType] {
		writeError(w, http.StatusBadRequest, "ActionParameterNotSupported",
			"invalid ResetType: "+req.ResetType)
		return
	}

	if err := s.machine.Reset(req.ResetType); err != nil {
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	if powerOnResetTypes[req.ResetType] {
		s.onSystemPowerOnReset(req.ResetType, id)
	}

	w.WriteHeader(http.StatusNoContent)
}

// onSystemPowerOnReset applies what a real system picks up during POST:
// pending BIOS settings and staged BIOS firmware.
func (s *Server) onSystemPowerOnReset(resetType, id string) {
	if applied := s.applyPendingBiosSettings(); len(applied) > 0 {
		s.debugf("BIOS: applied %d pending setting(s) on %s reset for system=%s: %v",
			len(applied), resetType, id, applied)
	}
	if v, ok := s.activateStagedFirmware(firmware.ComponentBIOS); ok {
		s.debugf("BIOS: activated staged firmware %s on %s reset for system=%s", v, resetType, id)
	}
}
