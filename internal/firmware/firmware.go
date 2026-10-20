// Package firmware simulates installing firmware update packages: it fetches a
// package from an ImageURI, validates it and reports progress while "flashing".
// Activation of the new version is left to the caller (a BMC applies staged
// firmware on the next host reset or BMC restart).
package firmware

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

// Component identifies the firmware a package installs.
type Component string

const (
	ComponentBIOS Component = "BIOS"
	ComponentBMC  Component = "BMC"
)

// maxPackageSize caps the downloaded package. Packages are tiny JSON documents,
// anything larger is not a qemu-bmc package.
const maxPackageSize = 64 << 10

// Package is the qemu-bmc firmware package format:
//
//	{"Component": "BIOS", "Version": "1.1.0"}
//
// The package declares its own component because clients such as
// metal-operator's Dell client send SimpleUpdate without Targets, leaving it to
// the BMC to determine what an image updates, like a Dell Update Package does.
type Package struct {
	Component Component `json:"Component"`
	Version   string    `json:"Version"`
}

// Parse decodes and validates a package.
func Parse(data []byte) (Package, error) {
	var pkg Package
	if err := json.Unmarshal(data, &pkg); err != nil {
		return Package{}, fmt.Errorf("invalid firmware package: %w", err)
	}
	switch pkg.Component {
	case ComponentBIOS, ComponentBMC:
	default:
		return Package{}, fmt.Errorf("invalid firmware package: unsupported Component %q", pkg.Component)
	}
	if pkg.Version == "" {
		return Package{}, errors.New("invalid firmware package: Version is required")
	}
	return pkg, nil
}

// Request describes one update to perform.
type Request struct {
	ImageURI string
	Username string
	Password string
	// Targets restricts the components the package may update. Empty means
	// any component, the package decides.
	Targets []Component
}

// Updater downloads and "flashes" firmware packages.
type Updater struct {
	client *http.Client
	// duration is the simulated flash time after the download.
	duration time.Duration
}

// NewUpdater returns an Updater downloading with the given timeout and
// simulating a flash of the given duration. insecureTLS skips certificate
// verification for HTTPS image URIs.
func NewUpdater(downloadTimeout, duration time.Duration, insecureTLS bool) *Updater {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecureTLS} //nolint:gosec // opt-in for lab file servers
	return &Updater{
		client:   &http.Client{Timeout: downloadTimeout, Transport: transport},
		duration: duration,
	}
}

// flashSteps is the number of progress reports during the simulated flash.
const flashSteps = 4

// Run downloads and validates the package, then simulates flashing it,
// calling progress with increasing percentages. It returns the installed
// package once flashing is complete.
func (u *Updater) Run(ctx context.Context, req Request, progress func(percent int)) (Package, error) {
	data, err := u.fetch(ctx, req)
	if err != nil {
		return Package{}, err
	}
	pkg, err := Parse(data)
	if err != nil {
		return Package{}, err
	}
	if len(req.Targets) > 0 && !slices.Contains(req.Targets, pkg.Component) {
		return Package{}, fmt.Errorf("firmware package for %s does not apply to targets %v", pkg.Component, req.Targets)
	}
	progress(10)

	step := u.duration / flashSteps
	for i := 1; i <= flashSteps; i++ {
		select {
		case <-ctx.Done():
			return Package{}, ctx.Err()
		case <-time.After(step):
		}
		if i < flashSteps {
			progress(10 + i*80/flashSteps)
		}
	}
	return pkg, nil
}

func (u *Updater) fetch(ctx context.Context, req Request) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.ImageURI, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid ImageURI: %w", err)
	}
	if req.Username != "" || req.Password != "" {
		httpReq.SetBasicAuth(req.Username, req.Password)
	}
	resp, err := u.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", req.ImageURI, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download %s: HTTP %d", req.ImageURI, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPackageSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", req.ImageURI, err)
	}
	if len(data) > maxPackageSize {
		return nil, fmt.Errorf("firmware package %s exceeds %d bytes", req.ImageURI, maxPackageSize)
	}
	return data, nil
}
