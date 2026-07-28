package redfish

// ODataID represents an OData reference
type ODataID struct {
	ODataID string `json:"@odata.id"`
}

// ServiceRoot is the Redfish service root
type ServiceRoot struct {
	ODataType      string  `json:"@odata.type"`
	ODataID        string  `json:"@odata.id"`
	ODataContext   string  `json:"@odata.context,omitempty"`
	ID             string  `json:"Id"`
	Name           string  `json:"Name"`
	RedfishVersion string  `json:"RedfishVersion"`
	Systems        ODataID `json:"Systems"`
	Managers       ODataID `json:"Managers"`
	Chassis        ODataID `json:"Chassis"`
}

// SystemCollection is a collection of computer systems
type SystemCollection struct {
	ODataType    string    `json:"@odata.type"`
	ODataID      string    `json:"@odata.id"`
	Name         string    `json:"Name"`
	MembersCount int       `json:"Members@odata.count"`
	Members      []ODataID `json:"Members"`
}

// ComputerSystem represents a computer system
type ComputerSystem struct {
	ODataType     string                `json:"@odata.type"`
	ODataID       string                `json:"@odata.id"`
	ODataContext  string                `json:"@odata.context,omitempty"`
	ODataEtag     string                `json:"@odata.etag,omitempty"`
	ID            string                `json:"Id"`
	Name          string                `json:"Name"`
	UUID          string                `json:"UUID,omitempty"`
	Manufacturer  string                `json:"Manufacturer,omitempty"`
	Model         string                `json:"Model,omitempty"`
	SKU           string                `json:"SKU,omitempty"`
	SerialNumber  string                `json:"SerialNumber,omitempty"`
	BiosVersion   string                `json:"BiosVersion,omitempty"`
	IndicatorLED  string                `json:"IndicatorLED,omitempty"`
	PowerState    string                `json:"PowerState"`
	Boot          BootSource            `json:"Boot"`
	MemorySummary MemorySummary         `json:"MemorySummary"`
	Processors    ODataID               `json:"Processors"`
	Actions       ComputerSystemActions `json:"Actions"`
	Links         ComputerSystemLinks   `json:"Links"`
}

// MemorySummary describes the central memory for a ComputerSystem.
type MemorySummary struct {
	TotalSystemMemoryGiB float64 `json:"TotalSystemMemoryGiB"`
}

// ComputerSystemLinks holds related-resource references for a ComputerSystem.
// Ironic's redfish inspect interface locates the managing BMC via Links/ManagedBy
// and refuses to start inspection if it is absent ("The attribute Links/ManagedBy
// is missing from the resource /redfish/v1/Systems/1"). Real BMCs always populate it.
type ComputerSystemLinks struct {
	ManagedBy []ODataID `json:"ManagedBy"`
}

// BootSource represents boot source override
type BootSource struct {
	BootSourceOverrideEnabled string   `json:"BootSourceOverrideEnabled"`
	BootSourceOverrideTarget  string   `json:"BootSourceOverrideTarget"`
	BootSourceOverrideMode    string   `json:"BootSourceOverrideMode"`
	AllowableValues           []string `json:"BootSourceOverrideTarget@Redfish.AllowableValues"`
}

// ComputerSystemActions contains available actions
type ComputerSystemActions struct {
	Reset ResetAction `json:"#ComputerSystem.Reset"`
}

// ResetAction describes the reset action
type ResetAction struct {
	Target          string   `json:"target"`
	AllowableValues []string `json:"ResetType@Redfish.AllowableValues"`
}

// ResetRequest is the request body for reset action
type ResetRequest struct {
	ResetType string `json:"ResetType"`
}

// PatchSystemRequest is the request body for patching a system
type PatchSystemRequest struct {
	Boot         *PatchBootSource `json:"Boot,omitempty"`
	IndicatorLED *string          `json:"IndicatorLED,omitempty"`
}

// PatchBootSource is the boot source in a patch request
type PatchBootSource struct {
	BootSourceOverrideEnabled string `json:"BootSourceOverrideEnabled,omitempty"`
	BootSourceOverrideTarget  string `json:"BootSourceOverrideTarget,omitempty"`
	BootSourceOverrideMode    string `json:"BootSourceOverrideMode,omitempty"`
}

// RedfishError is a Redfish error response
type RedfishError struct {
	Error RedfishErrorBody `json:"error"`
}

// RedfishErrorBody is the body of a Redfish error
type RedfishErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ManagerCollection is a collection of managers
type ManagerCollection struct {
	ODataType    string    `json:"@odata.type"`
	ODataID      string    `json:"@odata.id"`
	Name         string    `json:"Name"`
	MembersCount int       `json:"Members@odata.count"`
	Members      []ODataID `json:"Members"`
}

// Manager represents a BMC manager
type Manager struct {
	ODataType       string         `json:"@odata.type"`
	ODataID         string         `json:"@odata.id"`
	ODataContext    string         `json:"@odata.context,omitempty"`
	ID              string         `json:"Id"`
	Name            string         `json:"Name"`
	ManagerType     string         `json:"ManagerType"`
	Manufacturer    string         `json:"Manufacturer,omitempty"`
	Model           string         `json:"Model,omitempty"`
	SerialNumber    string         `json:"SerialNumber,omitempty"`
	PartNumber      string         `json:"PartNumber,omitempty"`
	FirmwareVersion string         `json:"FirmwareVersion,omitempty"`
	LastResetTime   string         `json:"LastResetTime,omitempty"`
	PowerState      string         `json:"PowerState,omitempty"`
	Status          ResourceStatus `json:"Status,omitempty"`
	VirtualMedia    ODataID        `json:"VirtualMedia"`
	Actions         ManagerActions `json:"Actions"`
}

// ManagerActions contains available actions for a Manager.
type ManagerActions struct {
	Reset ResetAction `json:"#Manager.Reset"`
}

// ResourceStatus is the standard Redfish Status object (State/Health). Clients
// like metal-operator map Manager.Status.State onto the BMC's state.
type ResourceStatus struct {
	State  string `json:"State,omitempty"`
	Health string `json:"Health,omitempty"`
}

// VirtualMediaCollection is a collection of virtual media
type VirtualMediaCollection struct {
	ODataType    string    `json:"@odata.type"`
	ODataID      string    `json:"@odata.id"`
	Name         string    `json:"Name"`
	MembersCount int       `json:"Members@odata.count"`
	Members      []ODataID `json:"Members"`
}

// VirtualMedia represents a virtual media resource
type VirtualMedia struct {
	ODataType    string              `json:"@odata.type"`
	ODataID      string              `json:"@odata.id"`
	ODataContext string              `json:"@odata.context,omitempty"`
	ID           string              `json:"Id"`
	Name         string              `json:"Name"`
	MediaTypes   []string            `json:"MediaTypes"`
	Image        string              `json:"Image,omitempty"`
	Inserted     bool                `json:"Inserted"`
	ConnectedVia string              `json:"ConnectedVia,omitempty"`
	Actions      VirtualMediaActions `json:"Actions"`
}

// VirtualMediaActions contains available actions for virtual media
type VirtualMediaActions struct {
	InsertMedia VirtualMediaAction `json:"#VirtualMedia.InsertMedia"`
	EjectMedia  VirtualMediaAction `json:"#VirtualMedia.EjectMedia"`
}

// VirtualMediaAction describes a virtual media action
type VirtualMediaAction struct {
	Target string `json:"target"`
}

// InsertMediaRequest is the request body for inserting virtual media
type InsertMediaRequest struct {
	Image    string `json:"Image"`
	Inserted bool   `json:"Inserted"`
}

// ChassisCollection is a collection of chassis
type ChassisCollection struct {
	ODataType    string    `json:"@odata.type"`
	ODataID      string    `json:"@odata.id"`
	Name         string    `json:"Name"`
	MembersCount int       `json:"Members@odata.count"`
	Members      []ODataID `json:"Members"`
}

// Chassis represents a chassis resource
type Chassis struct {
	ODataType    string `json:"@odata.type"`
	ODataID      string `json:"@odata.id"`
	ODataContext string `json:"@odata.context,omitempty"`
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	ChassisType  string `json:"ChassisType"`
}

// ProcessorCollection is a collection of processors
type ProcessorCollection struct {
	ODataType    string    `json:"@odata.type"`
	ODataID      string    `json:"@odata.id"`
	Name         string    `json:"Name"`
	MembersCount int       `json:"Members@odata.count"`
	Members      []ODataID `json:"Members"`
}

// Processor represents a single processor resource
type Processor struct {
	ODataType             string `json:"@odata.type"`
	ODataID               string `json:"@odata.id"`
	ODataContext          string `json:"@odata.context,omitempty"`
	ID                    string `json:"Id"`
	Name                  string `json:"Name"`
	ProcessorType         string `json:"ProcessorType"`
	ProcessorArchitecture string `json:"ProcessorArchitecture"`
	InstructionSet        string `json:"InstructionSet"`
	Manufacturer          string `json:"Manufacturer"`
	Model                 string `json:"Model"`
	MaxSpeedMHz           *int   `json:"MaxSpeedMHz,omitempty"`
	TotalCores            *int   `json:"TotalCores,omitempty"`
	TotalThreads          *int   `json:"TotalThreads,omitempty"`
}
