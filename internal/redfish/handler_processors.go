package redfish

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

func (s *Server) handleProcessorCollection(w http.ResponseWriter, r *http.Request) {
	col := ProcessorCollection{
		ODataType:    "#ProcessorCollection.ProcessorCollection",
		ODataID:      "/redfish/v1/Systems/1/Processors",
		Name:         "Processor Collection",
		MembersCount: 1,
		Members:      []ODataID{{ODataID: "/redfish/v1/Systems/1/Processors/1"}},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

func (s *Server) handleGetProcessor(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	if vars["procid"] != "1" {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "processor not found")
		return
	}

	// A single Processor resource models QEMU's own default `-smp N` topology:
	// one socket containing N cores, one thread per core.
	cores := s.inventory.CPUCount

	proc := Processor{
		ODataType:             "#Processor.v1_9_0.Processor",
		ODataID:               "/redfish/v1/Systems/1/Processors/1",
		ODataContext:          "/redfish/v1/$metadata#Processor.Processor",
		ID:                    "1",
		Name:                  "CPU",
		ProcessorType:         "CPU",
		ProcessorArchitecture: "x86",
		InstructionSet:        "x86-64",
		Manufacturer:          "Intel",
		Model:                 s.inventory.CPUModel,
		TotalCores:            &cores,
		TotalThreads:          &cores,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(proc)
}
