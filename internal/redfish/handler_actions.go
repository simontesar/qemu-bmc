package redfish

import (
	"encoding/json"
	"net/http"
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
// Reset() returns, i.e. these after which pending bios settings should be
// applied.
var powerOnResetTypes = map[string]bool{
	"On":              true,
	"ForceOn":         true,
	"ForceRestart":    true,
	"GracefulRestart": true,
	"PowerCycle":      true,
}

func (s *Server) handleResetAction(w http.ResponseWriter, r *http.Request) {
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
		s.applyPendingBiosSettings()
	}

	w.WriteHeader(http.StatusNoContent)
}
