package qemu

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ProcessManager controls the lifecycle of a QEMU process.
type ProcessManager interface {
	Start(bootTarget string) error
	Stop(timeout time.Duration) error
	Kill() error
	IsRunning() bool
	WaitForExit(timeout time.Duration) error
	ExitCh() <-chan struct{}
	// SetMedia records the virtual-media image (a file path or http(s) URL) to
	// attach to the ide0-cd0 CD drive the NEXT time QEMU cold-starts. Empty ejects.
	// This is what makes Redfish InsertMedia-while-powered-off work (Ironic's
	// virtual-media flow inserts media, then powers on): the live QMP change-medium
	// has no running QEMU to target, so the image is instead baked into the start args.
	SetMedia(image string)
}

// CommandFactory creates exec.Cmd instances. Allows test injection.
type CommandFactory func(binary string, args []string) *exec.Cmd

// DefaultCommandFactory creates a standard exec.Cmd.
func DefaultCommandFactory(binary string, args []string) *exec.Cmd {
	return exec.Command(binary, args...)
}

type processManager struct {
	binary     string
	baseArgs   []string
	cmdFactory CommandFactory
	cmd        *exec.Cmd
	running    bool
	media      string // virtual media attached to ide0-cd0 on the next cold start ("" = empty)
	exitCh     chan struct{}
	mu         sync.RWMutex
}

// SetMedia records the CD image to attach on the next cold start (see interface doc).
func (p *processManager) SetMedia(image string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.media = image
}

// applyMedia injects file=<media> into the empty ide0-cd0 cdrom drive arg so a
// cold-started QEMU boots the inserted virtual media. No-op if media is empty or the
// cdrom drive already carries a file. QEMU (7.2, http block driver) accepts an http(s)
// URL as the drive file, which is what Ironic's redfish-virtualmedia hands the BMC.
func applyMedia(args []string, media string) []string {
	if media == "" {
		return args
	}
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		if strings.Contains(a, "media=cdrom") && !strings.Contains(a, "file=") {
			out[i] = a + ",file=" + media
			break
		}
	}
	return out
}

// NewProcessManager creates a ProcessManager for the given QEMU binary and base arguments.
func NewProcessManager(binary string, baseArgs []string, factory CommandFactory) ProcessManager {
	return &processManager{
		binary:     binary,
		baseArgs:   baseArgs,
		cmdFactory: factory,
		exitCh:     make(chan struct{}),
	}
}

func (p *processManager) Start(bootTarget string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil // already running, no-op
	}

	args := ApplyBootOverride(p.baseArgs, bootTarget)
	args = applyMedia(args, p.media)
	p.cmd = p.cmdFactory(p.binary, args)
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("starting QEMU process: %w", err)
	}

	p.running = true
	p.exitCh = make(chan struct{})

	go p.monitor()
	return nil
}

func (p *processManager) monitor() {
	p.cmd.Wait()
	p.mu.Lock()
	p.running = false
	ch := p.exitCh
	p.mu.Unlock()
	close(ch)
}

func (p *processManager) Stop(timeout time.Duration) error {
	p.mu.RLock()
	if !p.running {
		p.mu.RUnlock()
		return nil
	}
	cmd := p.cmd
	p.mu.RUnlock()

	if cmd.Process == nil {
		return nil
	}

	// Send SIGTERM
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM: %w", err)
	}

	// Wait with timeout
	if err := p.WaitForExit(timeout); err != nil {
		// Timeout — force kill
		return p.Kill()
	}
	return nil
}

func (p *processManager) Kill() error {
	p.mu.RLock()
	if !p.running {
		p.mu.RUnlock()
		return nil
	}
	cmd := p.cmd
	p.mu.RUnlock()

	if cmd.Process == nil {
		return nil
	}

	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("sending SIGKILL: %w", err)
	}
	return nil
}

func (p *processManager) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

func (p *processManager) WaitForExit(timeout time.Duration) error {
	p.mu.RLock()
	ch := p.exitCh
	p.mu.RUnlock()

	select {
	case <-ch:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timeout waiting for process exit after %s", timeout)
	}
}

func (p *processManager) ExitCh() <-chan struct{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.exitCh
}
