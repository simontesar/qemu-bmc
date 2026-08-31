package redfish

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

//go:embed data/manager_attribute_registry.json
var managerAttributeRegistryJSON []byte

const managerAttributeRegistryID = "ManagerAttributeRegistry.v1_0_0"

// registryFile describes one served attribute registry: the raw registry
// content plus the metadata surfaced by the MessageRegistryFile envelope.
type registryFile struct {
	id       string
	name     string
	registry string // MessageRegistryFile.Registry (e.g. "BiosAttributeRegistry1.0")
	content  []byte
}

// registries is the set of attribute registries qemu-bmc serves under
// /redfish/v1/Registries. Both are consumed by metal-operator: the BIOS one by
// the generic Redfish client, the Manager one by the Dell iDRAC client
// (DellRedfishBMC) when applying BMCSettings.
var registries = []registryFile{
	{
		id:       biosAttributeRegistryID,
		name:     "BIOS Attribute Registry File",
		registry: "BiosAttributeRegistry1.0",
		content:  biosAttributeRegistryJSON,
	},
	{
		id:       managerAttributeRegistryID,
		name:     "Manager Attribute Registry File",
		registry: "ManagerAttributeRegistry1.0",
		content:  managerAttributeRegistryJSON,
	},
}

func lookupRegistry(id string) (registryFile, bool) {
	for _, reg := range registries {
		if reg.id == id {
			return reg, true
		}
	}
	return registryFile{}, false
}

func (s *Server) handleRegistryCollection(w http.ResponseWriter, r *http.Request) {
	members := make([]ODataID, 0, len(registries))
	for _, reg := range registries {
		members = append(members, ODataID{ODataID: "/redfish/v1/Registries/" + reg.id})
	}
	col := RegistryFileCollection{
		ODataType:    "#MessageRegistryFileCollection.MessageRegistryFileCollection",
		ODataID:      "/redfish/v1/Registries",
		Name:         "Registry File Collection",
		MembersCount: len(members),
		Members:      members,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

func (s *Server) handleGetRegistryFile(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	reg, ok := lookupRegistry(id)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "registry not found")
		return
	}

	file := MessageRegistryFile{
		ODataType: "#MessageRegistryFile.v1_1_4.MessageRegistryFile",
		ODataID:   "/redfish/v1/Registries/" + reg.id,
		ID:        reg.id,
		Name:      reg.name,
		Languages: []string{"en"},
		Registry:  reg.registry,
		Location: []RegistryLocation{
			{Language: "en", Uri: "/redfish/v1/Registries/" + reg.id + ".json"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(file)
}

func (s *Server) handleGetRegistryContent(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	reg, ok := lookupRegistry(id)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "registry not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(reg.content)
}
