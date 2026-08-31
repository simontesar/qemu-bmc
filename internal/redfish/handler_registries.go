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

// biosRegistryFile is always served under /redfish/v1/Registries. The generic
// Redfish client in metal-operator reads it for BIOSSettings.
var biosRegistryFile = registryFile{
	id:       biosAttributeRegistryID,
	name:     "BIOS Attribute Registry File",
	registry: "BiosAttributeRegistry1.0",
	content:  biosAttributeRegistryJSON,
}

// managerRegistryFile is the Dell iDRAC-style Manager attribute registry. It is
// served only when ENABLE_DELL_BMC_ATTRIBUTES is set (s.dellBMCAttributes).
var managerRegistryFile = registryFile{
	id:       managerAttributeRegistryID,
	name:     "Manager Attribute Registry File",
	registry: "ManagerAttributeRegistry1.0",
	content:  managerAttributeRegistryJSON,
}

// servedRegistries returns the registries this server currently exposes.
func (s *Server) servedRegistries() []registryFile {
	if s.dellBMCAttributes {
		return []registryFile{biosRegistryFile, managerRegistryFile}
	}
	return []registryFile{biosRegistryFile}
}

func (s *Server) lookupRegistry(id string) (registryFile, bool) {
	for _, reg := range s.servedRegistries() {
		if reg.id == id {
			return reg, true
		}
	}
	return registryFile{}, false
}

func (s *Server) handleRegistryCollection(w http.ResponseWriter, r *http.Request) {
	served := s.servedRegistries()
	members := make([]ODataID, 0, len(served))
	for _, reg := range served {
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
	reg, ok := s.lookupRegistry(id)
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
	reg, ok := s.lookupRegistry(id)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "registry not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(reg.content)
}
