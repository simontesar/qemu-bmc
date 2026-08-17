package machine

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tjst-t/qemu-bmc/internal/qmp"
)

// vmediaLocalPath is where an http(s) virtual-media image is downloaded before being attached
// to the ide0-cd0 CD. Booting directly from an http URL via QEMU's curl block driver issues a
// synchronous range request per sector — glacially slow for a multi-hundred-MB boot ISO — so we
// fetch the whole image to a local file first and attach that (boots at local disk speed).
const vmediaLocalPath = "/iso/vmedia.iso"

const defaultGracefulShutdownTimeout = 120 * time.Second

// fetchMedia downloads an http(s) URL to dst; a non-URL (local path) is returned unchanged.
func fetchMedia(image, dst string) (string, error) {
	if !strings.HasPrefix(image, "http://") && !strings.HasPrefix(image, "https://") {
		return image, nil // already a local file path
	}
	resp, err := http.Get(image)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", image, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: unexpected status %s", image, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", dst, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("write %s: %w", dst, err)
	}
	return dst, nil
}

// PowerState represents the power state of the VM
type PowerState string

const (
	PowerOn  PowerState = "On"
	PowerOff PowerState = "Off"
)

// BootOverride represents boot source override settings
type BootOverride struct {
	Enabled string // "Disabled", "Once", "Continuous"
	Target  string // "None", "Pxe", "Hdd", "Cd", "BiosSetup"
	Mode    string // "UEFI", "Legacy"
}

// ProcessManager controls the QEMU process lifecycle.
type ProcessManager interface {
	Start(bootTarget string) error
	Stop(timeout time.Duration) error
	Kill() error
	IsRunning() bool
	WaitForExit(timeout time.Duration) error
	SetMedia(image string)
}

// Machine manages the state of a QEMU VM
type Machine struct {
	qmpClient               qmp.Client
	processManager          ProcessManager // nil = legacy mode
	bootOverride            BootOverride
	gracefulShutdownTimeout time.Duration
	mu                      sync.RWMutex
}

// New creates a new Machine with the given QMP client (legacy mode)
func New(client qmp.Client) *Machine {
	return &Machine{
		qmpClient:               client,
		gracefulShutdownTimeout: defaultGracefulShutdownTimeout,
		bootOverride: BootOverride{
			Enabled: "Disabled",
			Target:  "None",
			Mode:    "UEFI",
		},
	}
}

// NewWithProcess creates a Machine in process management mode
func NewWithProcess(client qmp.Client, pm ProcessManager) *Machine {
	return &Machine{
		qmpClient:               client,
		processManager:          pm,
		gracefulShutdownTimeout: defaultGracefulShutdownTimeout,
		bootOverride: BootOverride{
			Enabled: "Disabled",
			Target:  "None",
			Mode:    "UEFI",
		},
	}
}

// SetGracefulShutdownTimeout overrides how long GracefulShutdown/GracefulRestart
// wait for the guest to exit on its own before falling back to a hard kill.
// Must be called before Reset is used concurrently.
func (m *Machine) SetGracefulShutdownTimeout(d time.Duration) {
	m.gracefulShutdownTimeout = d
}

// GetPowerState returns the current power state of the VM
func (m *Machine) GetPowerState() (PowerState, error) {
	if m.processManager != nil {
		return m.getPowerStateProcess()
	}
	return m.getPowerStateLegacy()
}

func (m *Machine) getPowerStateLegacy() (PowerState, error) {
	status, err := m.qmpClient.QueryStatus()
	if err != nil {
		return "", fmt.Errorf("querying VM status: %w", err)
	}

	switch status {
	case qmp.StatusRunning:
		return PowerOn, nil
	default:
		return PowerOff, nil
	}
}

func (m *Machine) getPowerStateProcess() (PowerState, error) {
	if !m.processManager.IsRunning() {
		return PowerOff, nil
	}

	status, err := m.qmpClient.QueryStatus()
	if err != nil {
		// QMP not ready yet, but process is running
		return PowerOn, nil
	}

	switch status {
	case qmp.StatusRunning, qmp.StatusPaused:
		return PowerOn, nil
	case qmp.StatusShutdown:
		// Guest has shut down — stop the process
		m.processManager.Stop(30 * time.Second)
		return PowerOff, nil
	default:
		return PowerOn, nil
	}
}

// GetQMPStatus returns the raw QMP status string
func (m *Machine) GetQMPStatus() (qmp.Status, error) {
	if m.processManager != nil {
		return m.getQMPStatusProcess()
	}
	return m.qmpClient.QueryStatus()
}

func (m *Machine) getQMPStatusProcess() (qmp.Status, error) {
	if !m.processManager.IsRunning() {
		return qmp.StatusShutdown, nil
	}

	status, err := m.qmpClient.QueryStatus()
	if err != nil {
		// QMP not ready yet, but process is running — report running
		return qmp.StatusRunning, nil
	}
	return status, nil
}

// Reset performs a reset action on the VM
func (m *Machine) Reset(resetType string) error {
	if m.processManager != nil {
		return m.resetProcessMode(resetType)
	}
	return m.resetLegacy(resetType)
}

func (m *Machine) resetLegacy(resetType string) error {
	switch resetType {
	case "On", "ForceOn":
		state, err := m.GetPowerState()
		if err != nil {
			return err
		}
		if state == PowerOn {
			return nil // already on, no-op
		}
		return m.qmpClient.Cont()
	case "ForceOff":
		return m.qmpClient.Stop()
	case "GracefulShutdown":
		// Send ACPI shutdown signal, then stop the VM.
		// The stop ensures the VM halts even without a guest OS.
		m.qmpClient.SystemPowerdown()
		return m.qmpClient.Stop()
	case "ForceRestart":
		return m.qmpClient.SystemReset()
	case "GracefulRestart":
		return m.qmpClient.SystemReset()
	case "PowerCycle":
		// Simulate a full power loss/restore (as opposed to ForceRestart's
		// in-place SystemReset) by pausing then resuming the VM.
		if err := m.qmpClient.Stop(); err != nil {
			return err
		}
		return m.qmpClient.Cont()
	default:
		return fmt.Errorf("unsupported reset type: %s", resetType)
	}
}

func (m *Machine) resetProcessMode(resetType string) error {
	switch resetType {
	case "On", "ForceOn":
		if m.processManager.IsRunning() {
			return nil // already running
		}
		m.mu.RLock()
		target := m.bootOverride.Target
		m.mu.RUnlock()

		if err := m.processManager.Start(target); err != nil {
			return fmt.Errorf("starting QEMU: %w", err)
		}

		if err := m.waitForQMP(30 * time.Second); err != nil {
			return fmt.Errorf("waiting for QMP: %w", err)
		}

		m.ConsumeBootOnce()
		return nil

	case "ForceOff":
		return m.forceOff()

	case "ForceRestart":
		return m.qmpClient.SystemReset()

	case "GracefulShutdown":
		if err := m.qmpClient.SystemPowerdown(); err != nil {
			return err
		}
		if err := m.processManager.WaitForExit(m.gracefulShutdownTimeout); err != nil {
			log.Printf("Graceful shutdown timed out: %v", err)
		}
		return nil

	case "GracefulRestart":
		if err := m.qmpClient.SystemPowerdown(); err != nil {
			return err
		}
		if err := m.processManager.WaitForExit(m.gracefulShutdownTimeout); err != nil {
			log.Printf("Graceful shutdown timed out, killing: %v", err)
			m.processManager.Kill()
			m.processManager.WaitForExit(5 * time.Second)
		}
		return m.resetProcessMode("On")

	case "PowerCycle":
		// Full power loss/restore: force the process down hard, then start
		// it back up. Distinct from GracefulRestart (ACPI shutdown first)
		// and ForceRestart (in-place SystemReset, no power loss).
		if err := m.forceOff(); err != nil {
			log.Printf("PowerCycle force-off failed, killing: %v", err)
			m.processManager.Kill()
			m.processManager.WaitForExit(5 * time.Second)
		}
		return m.resetProcessMode("On")

	default:
		return fmt.Errorf("unsupported reset type: %s", resetType)
	}
}

// forceOff hard-powers-down the process-managed VM: it asks QEMU to quit via
// QMP, falling back to killing the process if QMP isn't reachable.
func (m *Machine) forceOff() error {
	if err := m.qmpClient.Quit(); err != nil {
		// QMP may not be connected; force-kill the process
		log.Printf("QMP quit failed (%v), killing process", err)
		return m.processManager.Stop(30 * time.Second)
	}
	return m.processManager.WaitForExit(30 * time.Second)
}

// waitForQMP polls qmpClient.Connect() until success or timeout.
func (m *Machine) waitForQMP(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := m.qmpClient.Connect(); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("QMP connection timed out after %s", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// GetBootOverride returns the current boot override settings
func (m *Machine) GetBootOverride() BootOverride {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bootOverride
}

// SetBootOverride sets the boot override settings
func (m *Machine) SetBootOverride(override BootOverride) error {
	// Validate target
	validTargets := map[string]bool{
		"None": true, "Pxe": true, "Hdd": true, "Cd": true, "BiosSetup": true,
	}
	if !validTargets[override.Target] {
		return fmt.Errorf("invalid boot target: %s", override.Target)
	}

	// Validate enabled
	validEnabled := map[string]bool{
		"Disabled": true, "Once": true, "Continuous": true,
	}
	if !validEnabled[override.Enabled] {
		return fmt.Errorf("invalid boot enabled: %s", override.Enabled)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.bootOverride = override
	return nil
}

// ConsumeBootOnce consumes a "Once" boot override (resets to Disabled after use)
func (m *Machine) ConsumeBootOnce() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bootOverride.Enabled == "Once" {
		m.bootOverride.Enabled = "Disabled"
		m.bootOverride.Target = "None"
	}
}

// InsertMedia inserts virtual media into the VM. An http(s) image is downloaded to a local
// file first (see vmediaLocalPath) and the LOCAL path is attached — booting an ISO straight
// from an http URL via QEMU's curl block driver is impractically slow. It also persists the
// (local) media so a later cold start attaches it (Ironic's redfish-virtualmedia inserts media
// while the VM is powered OFF, then powers on — the live QMP change-medium below has no running
// QEMU to target in that case and fails non-fatally; the persisted media is what actually boots).
func (m *Machine) InsertMedia(image string) error {
	local, err := fetchMedia(image, vmediaLocalPath)
	if err != nil {
		return fmt.Errorf("fetching virtual media: %w", err)
	}
	if m.processManager != nil {
		m.processManager.SetMedia(local)
	}
	return m.qmpClient.BlockdevChangeMedium("ide0-cd0", local)
}

// EjectMedia ejects virtual media from the VM, clears the persisted cold-start media, and
// removes the downloaded local image file.
func (m *Machine) EjectMedia() error {
	if m.processManager != nil {
		m.processManager.SetMedia("")
	}
	_ = os.Remove(vmediaLocalPath)
	return m.qmpClient.BlockdevRemoveMedium("ide0-cd0")
}
