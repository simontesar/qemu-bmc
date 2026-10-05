package redfish

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/tjst-t/qemu-bmc/internal/bmc"
)

const (
	accountServicePath = "/redfish/v1/AccountService"
	accountsPath       = accountServicePath + "/Accounts"

	// User names and passwords are stored in the IPMI user table, so they are
	// bound by the IPMI 2.0 field sizes (16-byte name, 20-byte password).
	maxUserNameLength = 16
	minPasswordLength = 1
	maxPasswordLength = 20
)

// Redfish RoleId ↔ IPMI privilege level.
var rolePrivileges = map[string]uint8{
	"Administrator": 4,
	"Operator":      3,
	"ReadOnly":      2,
}

func roleForPrivilege(privilege uint8) string {
	switch {
	case privilege >= 4:
		return "Administrator"
	case privilege == 3:
		return "Operator"
	default:
		return "ReadOnly"
	}
}

func accountPath(id uint8) string {
	return fmt.Sprintf("%s/%d", accountsPath, id)
}

func toManagerAccount(a bmc.Account) ManagerAccount {
	return ManagerAccount{
		ODataType:    "#ManagerAccount.v1_4_0.ManagerAccount",
		ODataID:      accountPath(a.ID),
		ID:           strconv.Itoa(int(a.ID)),
		Name:         "User Account",
		UserName:     a.Name,
		RoleID:       roleForPrivilege(a.Privilege),
		Enabled:      a.Enabled,
		AccountTypes: []string{"Redfish", "IPMI"},
	}
}

func validateUserName(name string) error {
	if name == "" || len(name) > maxUserNameLength {
		return fmt.Errorf("UserName must be 1-%d bytes", maxUserNameLength)
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return fmt.Errorf("Password must be %d-%d bytes", minPasswordLength, maxPasswordLength)
	}
	return nil
}

func privilegeForRole(role string) (uint8, error) {
	privilege, ok := rolePrivileges[role]
	if !ok {
		return 0, fmt.Errorf("RoleId %q is not one of Administrator, Operator, ReadOnly", role)
	}
	return privilege, nil
}

// accountIDFromRequest parses the {id} path variable as a user slot number.
func accountIDFromRequest(r *http.Request) (uint8, bool) {
	id, err := strconv.ParseUint(mux.Vars(r)["id"], 10, 8)
	if err != nil {
		return 0, false
	}
	return uint8(id), true
}

func writeAccount(w http.ResponseWriter, statusCode int, a bmc.Account) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(toManagerAccount(a))
}

func (s *Server) handleGetAccountService(w http.ResponseWriter, r *http.Request) {
	svc := AccountService{
		ODataType:         "#AccountService.v1_5_0.AccountService",
		ODataID:           accountServicePath,
		ID:                "AccountService",
		Name:              "Account Service",
		ServiceEnabled:    true,
		MinPasswordLength: minPasswordLength,
		MaxPasswordLength: maxPasswordLength,
		Accounts:          ODataID{ODataID: accountsPath},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(svc)
}

func (s *Server) handleAccountCollection(w http.ResponseWriter, r *http.Request) {
	accounts := s.users.Accounts()
	members := make([]ODataID, 0, len(accounts))
	for _, a := range accounts {
		members = append(members, ODataID{ODataID: accountPath(a.ID)})
	}
	col := ManagerAccountCollection{
		ODataType:    "#ManagerAccountCollection.ManagerAccountCollection",
		ODataID:      accountsPath,
		Name:         "Accounts Collection",
		MembersCount: len(members),
		Members:      members,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(col)
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "invalid request body")
		return
	}
	if err := validateUserName(req.UserName); err != nil {
		writeError(w, http.StatusBadRequest, "PropertyValueError", err.Error())
		return
	}
	if err := validatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "PropertyValueError", err.Error())
		return
	}
	privilege, err := privilegeForRole(req.RoleID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PropertyValueNotInList", err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	id, err := s.users.CreateAccount(req.UserName, req.Password, privilege, enabled)
	switch {
	case errors.Is(err, bmc.ErrUserExists):
		writeError(w, http.StatusConflict, "ResourceAlreadyExists", "an account with this UserName already exists")
		return
	case errors.Is(err, bmc.ErrNoFreeSlot):
		writeError(w, http.StatusBadRequest, "CreateLimitReachedForResource", "no free account slots")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	s.debugf("AccountService: created account %q in slot %d (role=%s, enabled=%t)", req.UserName, id, req.RoleID, enabled)

	a, _ := s.users.Account(id)
	w.Header().Set("Location", accountPath(id))
	// gofish decodes the created ManagerAccount from the response body.
	writeAccount(w, http.StatusCreated, a)
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := accountIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "account not found")
		return
	}
	a, ok := s.users.Account(id)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "account not found")
		return
	}
	writeAccount(w, http.StatusOK, a)
}

func (s *Server) handlePatchAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := accountIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "account not found")
		return
	}
	var req PatchAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedJSON", "invalid request body")
		return
	}
	if req.UserName == nil && req.Password == nil && req.RoleID == nil && req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "PropertyMissing", "no patchable properties provided")
		return
	}

	update := bmc.AccountUpdate{Name: req.UserName, Password: req.Password, Enabled: req.Enabled}
	if req.UserName != nil {
		if err := validateUserName(*req.UserName); err != nil {
			writeError(w, http.StatusBadRequest, "PropertyValueError", err.Error())
			return
		}
	}
	if req.Password != nil {
		if err := validatePassword(*req.Password); err != nil {
			writeError(w, http.StatusBadRequest, "PropertyValueError", err.Error())
			return
		}
	}
	if req.RoleID != nil {
		privilege, err := privilegeForRole(*req.RoleID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "PropertyValueNotInList", err.Error())
			return
		}
		update.Privilege = &privilege
	}

	err := s.users.UpdateAccount(id, update)
	switch {
	case errors.Is(err, bmc.ErrNotFound):
		writeError(w, http.StatusNotFound, "ResourceNotFound", "account not found")
		return
	case errors.Is(err, bmc.ErrUserExists):
		writeError(w, http.StatusConflict, "ResourceAlreadyExists", "an account with this UserName already exists")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	s.debugf("AccountService: updated account in slot %d (userName=%t, password=%t, role=%t, enabled=%t)",
		id, req.UserName != nil, req.Password != nil, req.RoleID != nil, req.Enabled != nil)

	a, _ := s.users.Account(id)
	writeAccount(w, http.StatusOK, a)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := accountIDFromRequest(r)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "account not found")
		return
	}
	if err := s.users.DeleteAccount(id); err != nil {
		writeError(w, http.StatusNotFound, "ResourceNotFound", "account not found")
		return
	}
	s.debugf("AccountService: deleted account in slot %d", id)
	w.WriteHeader(http.StatusNoContent)
}
