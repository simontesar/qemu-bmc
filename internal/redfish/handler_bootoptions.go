package redfish

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

// bootOptionCatalog is the fixed set of devices this QEMU-backed system can
// actually boot — the same "Pxe"/"Hdd"/"Cd" targets ApplyBootOverride maps to
// QEMU -boot values, and the strings the default/settable Boot.BootOrder
// already uses. Exposing them as a proper BootOptions collection lets a
// client discover valid Boot.BootOrder entries instead of guessing them,
// mirroring a real BMC's /Systems/{id}/BootOptions.
var bootOptionCatalog = []struct {
	reference   string
	displayName string
}{
	{"Pxe", "PXE Network Boot"},
	{"Hdd", "Hard Disk"},
	{"Cd", "CD/DVD"},
}

func (s *Server) handleBootOptionCollection(w http.ResponseWriter, r *http.Request) {
	members := make([]ODataID, len(bootOptionCatalog))
	for i, opt := range bootOptionCatalog {
		members[i] = ODataID{ODataID: "/redfish/v1/Systems/1/BootOptions/" + opt.reference}
	}
	col := BootOptionCollection{
		ODataType:    "#BootOptionCollection.BootOptionCollection",
		ODataID:      "/redfish/v1/Systems/1/BootOptions",
		Name:         "Boot Options Collection",
		MembersCount: len(members),
		Members:      members,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

func (s *Server) handleGetBootOption(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	for _, opt := range bootOptionCatalog {
		if vars["bootoptid"] != opt.reference {
			continue
		}
		bo := BootOption{
			ODataType:           "#BootOption.v1_0_0.BootOption",
			ODataID:             "/redfish/v1/Systems/1/BootOptions/" + opt.reference,
			ID:                  opt.reference,
			Name:                "Boot Option",
			DisplayName:         opt.displayName,
			BootOptionReference: opt.reference,
			BootOptionEnabled:   true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bo)
		return
	}
	writeError(w, http.StatusNotFound, "ResourceNotFound", "boot option not found")
}
