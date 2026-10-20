package firmware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	pkg, err := Parse([]byte(`{"Component":"BIOS","Version":"1.1.0"}`))
	require.NoError(t, err)
	assert.Equal(t, Package{Component: ComponentBIOS, Version: "1.1.0"}, pkg)

	for name, body := range map[string]string{
		"not json":          `not json`,
		"unknown component": `{"Component":"NIC","Version":"1.1.0"}`,
		"missing version":   `{"Component":"BMC"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(body))
			assert.Error(t, err)
		})
	}
}

func packageServer(t *testing.T, user, pass string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != "" {
			if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		switch r.URL.Path {
		case "/bios-1.1.0.json":
			w.Write([]byte(`{"Component":"BIOS","Version":"1.1.0"}`))
		case "/bmc-1.1.0.json":
			w.Write([]byte(`{"Component":"BMC","Version":"1.1.0"}`))
		case "/huge.json":
			w.Write([]byte(strings.Repeat(" ", maxPackageSize+1)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdaterRun(t *testing.T) {
	srv := packageServer(t, "", "")
	u := NewUpdater(time.Second, 40*time.Millisecond, false)

	var progress []int
	pkg, err := u.Run(context.Background(), Request{ImageURI: srv.URL + "/bios-1.1.0.json"}, func(p int) {
		progress = append(progress, p)
	})
	require.NoError(t, err)
	assert.Equal(t, Package{Component: ComponentBIOS, Version: "1.1.0"}, pkg)
	assert.Equal(t, []int{10, 30, 50, 70}, progress)
}

func TestUpdaterRun_BasicAuth(t *testing.T) {
	srv := packageServer(t, "repo", "secret")
	u := NewUpdater(time.Second, 0, false)

	_, err := u.Run(context.Background(), Request{ImageURI: srv.URL + "/bmc-1.1.0.json"}, func(int) {})
	assert.ErrorContains(t, err, "HTTP 401")

	pkg, err := u.Run(context.Background(), Request{
		ImageURI: srv.URL + "/bmc-1.1.0.json", Username: "repo", Password: "secret",
	}, func(int) {})
	require.NoError(t, err)
	assert.Equal(t, ComponentBMC, pkg.Component)
}

func TestUpdaterRun_Failures(t *testing.T) {
	srv := packageServer(t, "", "")
	u := NewUpdater(time.Second, 0, false)

	for name, tc := range map[string]struct {
		req  Request
		want string
	}{
		"not found":       {Request{ImageURI: srv.URL + "/missing.json"}, "HTTP 404"},
		"too large":       {Request{ImageURI: srv.URL + "/huge.json"}, "exceeds"},
		"target mismatch": {Request{ImageURI: srv.URL + "/bios-1.1.0.json", Targets: []Component{ComponentBMC}}, "does not apply"},
		"unreachable":     {Request{ImageURI: "http://127.0.0.1:1/bios.json"}, "failed to download"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := u.Run(context.Background(), tc.req, func(int) {})
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestUpdaterRun_TargetMatch(t *testing.T) {
	srv := packageServer(t, "", "")
	u := NewUpdater(time.Second, 0, false)

	pkg, err := u.Run(context.Background(), Request{
		ImageURI: srv.URL + "/bios-1.1.0.json", Targets: []Component{ComponentBIOS},
	}, func(int) {})
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", pkg.Version)
}
