package redfish

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

//go:embed data/bios_attribute_registry.json
var biosAttributeRegistryJSON []byte

const biosAttributeRegistryID = "BiosAttributeRegistry.v1_0_0"

// biosAttributeResetRequired maps each known BIOS attribute name to whether
// changing it requires a reboot.
var biosAttributeResetRequired map[string]bool

func init() {
	var reg AttributeRegistry
	if err := json.Unmarshal(biosAttributeRegistryJSON, &reg); err != nil {
		panic("redfish: invalid embedded BIOS attribute registry: " + err.Error())
	}
	biosAttributeResetRequired = make(map[string]bool, len(reg.RegistryEntries.Attributes))
	for _, attr := range reg.RegistryEntries.Attributes {
		biosAttributeResetRequired[attr.AttributeName] = attr.ResetRequired
	}
}

func biosSettingsObjectPath(systemID string) string {
	return "/redfish/v1/Systems/" + systemID + "/Bios/Settings"
}

func (s *Server) handleGetBios(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	bios := Bios{
		ODataType:         "#Bios.v1_2_2.Bios",
		ODataID:           "/redfish/v1/Systems/" + id + "/Bios",
		ID:                "Bios",
		Name:              "BIOS Configuration Current Settings",
		AttributeRegistry: biosAttributeRegistryID,
		Attributes:        s.getBiosAttributes(),
		RedfishSettings: &RedfishSettings{
			ODataType:      "#Settings.v1_3_5.Settings",
			SettingsObject: ODataID{ODataID: biosSettingsObjectPath(id)},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bios)
}

func (s *Server) handleGetBiosSettings(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	bios := Bios{
		ODataType:         "#Bios.v1_2_2.Bios",
		ODataID:           biosSettingsObjectPath(id),
		ID:                "Settings",
		Name:              "BIOS Configuration Pending Settings",
		AttributeRegistry: biosAttributeRegistryID,
		Attributes:        s.getBiosPendingAttributes(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bios)
}

// handlePatchBiosSettings applies attributes that don't require a reboot
// immediately (to the live /Bios resource) and stashes reboot-required
// attributes as pending, to be applied by applyPendingBiosSettings once the
// system is next reset. An attribute absent from the embedded registry is
// treated as reboot-required, matching how metal-operator's own client
// treats a missing/null ResetRequired.
func (s *Server) handlePatchBiosSettings(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req PatchBiosSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "invalid request body")
		return
	}
	if len(req.Attributes) == 0 {
		writeError(w, http.StatusBadRequest, "PropertyMissing", "no attributes provided")
		return
	}

	pending := make(map[string]any, len(req.Attributes))
	for name, value := range req.Attributes {
		if resetRequired, known := biosAttributeResetRequired[name]; known && !resetRequired {
			s.setBiosAttribute(name, value)
			s.debugf("BIOS PATCH system=%s: %s=%v applied immediately (no reset required)", id, name, value)
			continue
		}
		pending[name] = value
		s.debugf("BIOS PATCH system=%s: %s=%v queued as pending (reset required)", id, name, value)
	}
	if len(pending) > 0 {
		s.mergeBiosPendingAttributes(pending)
	}
	s.debugf("BIOS PATCH system=%s: current=%v pending=%v", id, s.getBiosAttributes(), s.getBiosPendingAttributes())

	w.WriteHeader(http.StatusNoContent)
}
