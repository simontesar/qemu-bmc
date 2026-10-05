package redfish

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tjst-t/qemu-bmc/internal/bmc"
	"github.com/tjst-t/qemu-bmc/internal/qmp"
)

// doAccountRequest sends a request with optional basic auth credentials.
func doAccountRequest(srv *Server, method, path, body, user, pass string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestServiceRoot_LinksAccountService(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "", "", "")

	w := doAccountRequest(srv, "GET", "/redfish/v1", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var root ServiceRoot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &root))
	assert.Equal(t, "/redfish/v1/AccountService", root.AccountService.ODataID)
}

func TestGetAccountService(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "", "", "")

	w := doAccountRequest(srv, "GET", "/redfish/v1/AccountService", "", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var svc AccountService
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &svc))
	assert.Equal(t, "#AccountService.v1_5_0.AccountService", svc.ODataType)
	assert.True(t, svc.ServiceEnabled)
	assert.Equal(t, 1, svc.MinPasswordLength)
	assert.Equal(t, 20, svc.MaxPasswordLength)
	assert.Equal(t, "/redfish/v1/AccountService/Accounts", svc.Accounts.ODataID)
}

func TestAccountCollection_ListsDefaultAdmin(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")

	w := doAccountRequest(srv, "GET", "/redfish/v1/AccountService/Accounts", "", "admin", "password")
	require.Equal(t, http.StatusOK, w.Code)
	var col ManagerAccountCollection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &col))
	require.Equal(t, 1, col.MembersCount)
	assert.Equal(t, "/redfish/v1/AccountService/Accounts/2", col.Members[0].ODataID)

	w = doAccountRequest(srv, "GET", "/redfish/v1/AccountService/Accounts/2", "", "admin", "password")
	require.Equal(t, http.StatusOK, w.Code)
	var acc ManagerAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &acc))
	assert.Equal(t, "2", acc.ID)
	assert.Equal(t, "admin", acc.UserName)
	assert.Equal(t, "Administrator", acc.RoleID)
	assert.True(t, acc.Enabled)
	assert.Contains(t, w.Body.String(), `"Password":null`)
}

func TestCreateAccount(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")

	w := doAccountRequest(srv, "POST", "/redfish/v1/AccountService/Accounts",
		`{"UserName":"mtfuser","Password":"Abc123!x","RoleId":"ReadOnly"}`, "admin", "password")
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "/redfish/v1/AccountService/Accounts/3", w.Header().Get("Location"))

	// gofish decodes the created account from the POST response body.
	var acc ManagerAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &acc))
	assert.Equal(t, "/redfish/v1/AccountService/Accounts/3", acc.ODataID)
	assert.Equal(t, "3", acc.ID)
	assert.Equal(t, "mtfuser", acc.UserName)
	assert.Equal(t, "ReadOnly", acc.RoleID)
	assert.True(t, acc.Enabled, "Enabled defaults to true")
	assert.Nil(t, acc.Password)

	t.Run("Enabled=false is honoured", func(t *testing.T) {
		w := doAccountRequest(srv, "POST", "/redfish/v1/AccountService/Accounts",
			`{"UserName":"off","Password":"x","RoleId":"Operator","Enabled":false}`, "admin", "password")
		require.Equal(t, http.StatusCreated, w.Code)
		var acc ManagerAccount
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &acc))
		assert.False(t, acc.Enabled)
		assert.Equal(t, "Operator", acc.RoleID)
	})
}

func TestCreateAccount_Errors(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")

	tests := []struct {
		name string
		body string
		code int
		err  string
	}{
		{"malformed JSON", `{`, http.StatusBadRequest, "MalformedJSON"},
		{"missing user name", `{"Password":"x","RoleId":"ReadOnly"}`, http.StatusBadRequest, "PropertyValueError"},
		{"user name too long", `{"UserName":"abcdefghijklmnopq","Password":"x","RoleId":"ReadOnly"}`, http.StatusBadRequest, "PropertyValueError"},
		{"missing password", `{"UserName":"u","RoleId":"ReadOnly"}`, http.StatusBadRequest, "PropertyValueError"},
		{"password too long", `{"UserName":"u","Password":"123456789012345678901","RoleId":"ReadOnly"}`, http.StatusBadRequest, "PropertyValueError"},
		{"unknown role", `{"UserName":"u","Password":"x","RoleId":"Root"}`, http.StatusBadRequest, "PropertyValueNotInList"},
		{"duplicate user name", `{"UserName":"admin","Password":"x","RoleId":"ReadOnly"}`, http.StatusConflict, "ResourceAlreadyExists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doAccountRequest(srv, "POST", "/redfish/v1/AccountService/Accounts", tt.body, "admin", "password")
			assert.Equal(t, tt.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.err)
		})
	}
}

func TestCreateAccount_NoFreeSlot(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "", "", "")
	users := bmc.NewState("admin", "password")
	srv.SetUserStore(users)
	for i := 3; i <= int(users.MaxUsers()); i++ {
		_, err := users.CreateAccount(strings.Repeat("u", i), "x", 2, true)
		require.NoError(t, err)
	}

	w := doAccountRequest(srv, "POST", "/redfish/v1/AccountService/Accounts",
		`{"UserName":"full","Password":"x","RoleId":"ReadOnly"}`, "", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "CreateLimitReachedForResource")
}

func TestPatchAccount(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")
	w := doAccountRequest(srv, "POST", "/redfish/v1/AccountService/Accounts",
		`{"UserName":"mtfuser","Password":"old","RoleId":"ReadOnly"}`, "admin", "password")
	require.Equal(t, http.StatusCreated, w.Code)

	w = doAccountRequest(srv, "PATCH", "/redfish/v1/AccountService/Accounts/3",
		`{"RoleId":"Operator","Enabled":false}`, "admin", "password")
	require.Equal(t, http.StatusOK, w.Code)
	var acc ManagerAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &acc))
	assert.Equal(t, "Operator", acc.RoleID)
	assert.False(t, acc.Enabled)
	assert.Equal(t, "mtfuser", acc.UserName)

	tests := []struct {
		name string
		path string
		body string
		code int
		err  string
	}{
		{"empty body", "/redfish/v1/AccountService/Accounts/3", `{}`, http.StatusBadRequest, "PropertyMissing"},
		{"empty user name", "/redfish/v1/AccountService/Accounts/3", `{"UserName":""}`, http.StatusBadRequest, "PropertyValueError"},
		{"password too long", "/redfish/v1/AccountService/Accounts/3", `{"Password":"123456789012345678901"}`, http.StatusBadRequest, "PropertyValueError"},
		{"unknown role", "/redfish/v1/AccountService/Accounts/3", `{"RoleId":"Root"}`, http.StatusBadRequest, "PropertyValueNotInList"},
		{"duplicate user name", "/redfish/v1/AccountService/Accounts/3", `{"UserName":"admin"}`, http.StatusConflict, "ResourceAlreadyExists"},
		{"free slot", "/redfish/v1/AccountService/Accounts/9", `{"Password":"x"}`, http.StatusNotFound, "ResourceNotFound"},
		{"invalid id", "/redfish/v1/AccountService/Accounts/abc", `{"Password":"x"}`, http.StatusNotFound, "ResourceNotFound"},
	}
	srv2 := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")
	doAccountRequest(srv2, "POST", "/redfish/v1/AccountService/Accounts",
		`{"UserName":"mtfuser","Password":"old","RoleId":"ReadOnly"}`, "admin", "password")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doAccountRequest(srv2, "PATCH", tt.path, tt.body, "admin", "password")
			assert.Equal(t, tt.code, w.Code)
			assert.Contains(t, w.Body.String(), tt.err)
		})
	}
}

func TestDeleteAccount(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")
	w := doAccountRequest(srv, "POST", "/redfish/v1/AccountService/Accounts",
		`{"UserName":"mtfuser","Password":"x","RoleId":"ReadOnly"}`, "admin", "password")
	require.Equal(t, http.StatusCreated, w.Code)

	w = doAccountRequest(srv, "DELETE", "/redfish/v1/AccountService/Accounts/3", "", "admin", "password")
	assert.Equal(t, http.StatusNoContent, w.Code)

	w = doAccountRequest(srv, "GET", "/redfish/v1/AccountService/Accounts/3", "", "admin", "password")
	assert.Equal(t, http.StatusNotFound, w.Code)

	w = doAccountRequest(srv, "DELETE", "/redfish/v1/AccountService/Accounts/3", "", "admin", "password")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAccountLifecycle_MetalOperatorFlow replays the request sequence that
// metal-operator's AccountManager (driven by metal-maintenance-operator's
// BMCUser controller) sends, with basic auth enforced.
func TestAccountLifecycle_MetalOperatorFlow(t *testing.T) {
	srv := NewServer(newMockMachine(qmp.StatusRunning), "admin", "password", "")
	const svcPath = "/redfish/v1/AccountService"

	// GetAccountService / GetAccounts
	require.Equal(t, http.StatusOK, doAccountRequest(srv, "GET", svcPath, "", "admin", "password").Code)
	require.Equal(t, http.StatusOK, doAccountRequest(srv, "GET", svcPath+"/Accounts", "", "admin", "password").Code)

	// The new user cannot log in before it exists.
	assert.Equal(t, http.StatusUnauthorized, doAccountRequest(srv, "GET", svcPath, "", "mtfuser", "Abc123!x").Code)

	// CreateOrUpdateAccount: POST
	w := doAccountRequest(srv, "POST", svcPath+"/Accounts",
		`{"UserName":"mtfuser","Password":"Abc123!x","RoleId":"ReadOnly","Enabled":true}`, "admin", "password")
	require.Equal(t, http.StatusCreated, w.Code)
	var acc ManagerAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &acc))

	// bmcConnectionTest: log in as the new account and read the AccountService.
	assert.Equal(t, http.StatusOK, doAccountRequest(srv, "GET", svcPath, "", "mtfuser", "Abc123!x").Code)

	// Password rotation: CreateOrUpdateAccount PATCHes only the changed field.
	w = doAccountRequest(srv, "PATCH", acc.ODataID, `{"Password":"Xyz789?q"}`, "admin", "password")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, http.StatusUnauthorized, doAccountRequest(srv, "GET", svcPath, "", "mtfuser", "Abc123!x").Code)
	assert.Equal(t, http.StatusOK, doAccountRequest(srv, "GET", svcPath, "", "mtfuser", "Xyz789?q").Code)

	// DeleteAccount: DELETE the account's @odata.id.
	require.Equal(t, http.StatusNoContent, doAccountRequest(srv, "DELETE", acc.ODataID, "", "admin", "password").Code)
	assert.Equal(t, http.StatusUnauthorized, doAccountRequest(srv, "GET", svcPath, "", "mtfuser", "Xyz789?q").Code)

	// Only the bootstrap admin remains.
	w = doAccountRequest(srv, "GET", svcPath+"/Accounts", "", "admin", "password")
	var col ManagerAccountCollection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &col))
	assert.Equal(t, 1, col.MembersCount)
}
