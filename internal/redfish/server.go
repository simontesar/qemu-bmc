package redfish

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/tjst-t/qemu-bmc/internal/machine"
	"github.com/tjst-t/qemu-bmc/internal/novnc"
	"github.com/tjst-t/qemu-bmc/internal/qmp"
)

// MachineInterface defines what the Redfish server needs from the machine layer
type MachineInterface interface {
	GetPowerState() (machine.PowerState, error)
	GetQMPStatus() (qmp.Status, error)
	Reset(resetType string) error
	GetBootOverride() machine.BootOverride
	SetBootOverride(override machine.BootOverride) error
	InsertMedia(image string) error
	EjectMedia() error
}

// Inventory holds the static ComputerSystem/Manager/Processor inventory data
// surfaced by the Redfish API. It's set once via SetInventory before the server
// starts handling requests, so it needs no locking.
type Inventory struct {
	// ComputerSystem inventory surfaced at /redfish/v1/Systems/1.
	SystemUUID         string
	SystemManufacturer string
	SystemModel        string
	SystemSerial       string
	SystemSKU          string
	SystemBiosVersion  string
	// Manager inventory surfaced at /redfish/v1/Managers/1. Clients such as
	// metal-operator read Manager.Model/FirmwareVersion, not ComputerSystem's.
	ManagerModel           string
	ManagerFirmwareVersion string
	ManagerManufacturer    string
	ManagerSerial          string
	ManagerPartNumber      string
	// Processor inventory surfaced at /redfish/v1/Systems/1/Processors/1.
	CPUModel string
	CPUCount int
	// Memory inventory surfaced at /redfish/v1/Systems/1 (MemorySummary).
	MemoryMiB int
}

// Server is the Redfish HTTP server
type Server struct {
	router       *mux.Router
	machine      MachineInterface
	user         string
	pass         string
	novncHandler *novnc.Handler
	inventory    Inventory

	// mu guards the runtime-mutable fields below. Chainsaw's own test runs are
	// serialized (--parallel 1), but the server itself may be polled/patched
	// concurrently by other real clients (Ironic, metal-operator, a human curl).
	mu            sync.RWMutex
	currentMedia  string
	indicatorLED  string
	lastResetTime time.Time
}

// SetInventory populates the static ComputerSystem/Manager/Processor inventory
// returned by the Redfish API. Clients such as metal-operator require a
// non-empty ComputerSystem UUID for server discovery.
func (s *Server) SetInventory(inv Inventory) {
	s.inventory = inv
}

func (s *Server) getCurrentMedia() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentMedia
}

func (s *Server) setCurrentMedia(image string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentMedia = image
}

func (s *Server) getIndicatorLED() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indicatorLED
}

func (s *Server) setIndicatorLED(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.indicatorLED = state
}

func (s *Server) getLastResetTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastResetTime
}

func (s *Server) setLastResetTime(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastResetTime = t
}

// NewServer creates a new Redfish server
func NewServer(m MachineInterface, user, pass, vncAddr string) *Server {
	s := &Server{
		router:       mux.NewRouter(),
		machine:      m,
		user:         user,
		pass:         pass,
		novncHandler: novnc.NewHandler(vncAddr),
		indicatorLED: "Off",
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// Apply middleware
	s.router.Use(s.trailingSlashMiddleware)
	if s.user != "" && s.pass != "" {
		s.router.Use(s.basicAuthMiddleware)
	}

	// Service Root
	s.router.HandleFunc("/redfish/v1", s.handleServiceRoot).Methods("GET")
	s.router.HandleFunc("/redfish/v1/", s.handleServiceRoot).Methods("GET")

	// Systems
	s.router.HandleFunc("/redfish/v1/Systems", s.handleSystemCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Systems/", s.handleSystemCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Systems/{id}", s.handleGetSystem).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Systems/{id}/", s.handleGetSystem).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Systems/{id}", s.handlePatchSystem).Methods("PATCH")
	s.router.HandleFunc("/redfish/v1/Systems/{id}/", s.handlePatchSystem).Methods("PATCH")

	// Actions
	s.router.HandleFunc("/redfish/v1/Systems/{id}/Actions/ComputerSystem.Reset", s.handleResetAction).Methods("POST")
	s.router.HandleFunc("/redfish/v1/Systems/{id}/Actions/ComputerSystem.Reset/", s.handleResetAction).Methods("POST")

	// Processors
	s.router.HandleFunc("/redfish/v1/Systems/{id}/Processors", s.handleProcessorCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Systems/{id}/Processors/", s.handleProcessorCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Systems/{id}/Processors/{procid}", s.handleGetProcessor).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Systems/{id}/Processors/{procid}/", s.handleGetProcessor).Methods("GET")

	// Managers
	s.router.HandleFunc("/redfish/v1/Managers", s.handleManagerCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Managers/", s.handleManagerCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Managers/{id}", s.handleGetManager).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/", s.handleGetManager).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/Actions/Manager.Reset", s.handleManagerReset).Methods("POST")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/Actions/Manager.Reset/", s.handleManagerReset).Methods("POST")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/VirtualMedia", s.handleVirtualMediaCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/VirtualMedia/", s.handleVirtualMediaCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/VirtualMedia/{vmid}", s.handleGetVirtualMedia).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/VirtualMedia/{vmid}/", s.handleGetVirtualMedia).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/VirtualMedia/{vmid}/Actions/VirtualMedia.InsertMedia", s.handleInsertMedia).Methods("POST")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/VirtualMedia/{vmid}/Actions/VirtualMedia.InsertMedia/", s.handleInsertMedia).Methods("POST")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/VirtualMedia/{vmid}/Actions/VirtualMedia.EjectMedia", s.handleEjectMedia).Methods("POST")
	s.router.HandleFunc("/redfish/v1/Managers/{id}/VirtualMedia/{vmid}/Actions/VirtualMedia.EjectMedia/", s.handleEjectMedia).Methods("POST")

	// Chassis
	s.router.HandleFunc("/redfish/v1/Chassis", s.handleChassisCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Chassis/", s.handleChassisCollection).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Chassis/{id}", s.handleGetChassis).Methods("GET")
	s.router.HandleFunc("/redfish/v1/Chassis/{id}/", s.handleGetChassis).Methods("GET")

	// noVNC: redirect /novnc/ → /novnc/vnc.html, serve static files, and WebSocket proxy
	s.router.HandleFunc("/novnc/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/novnc/vnc.html", http.StatusFound)
	}).Methods("GET")
	s.router.PathPrefix("/novnc/").Handler(
		http.StripPrefix("/novnc/", s.novncHandler.ServeFiles()),
	)
	s.router.HandleFunc("/websockify", s.novncHandler.ServeWebSocket)
}

// ServeHTTP implements the http.Handler interface
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}
