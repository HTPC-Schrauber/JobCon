package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"jobcon/internal/auth"
	"jobcon/internal/db"
	"net/http"

	"github.com/google/uuid"
)

type CreateUserRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        string `json:"role"`
}

type UpdatePasswordRequest struct {
	NewPassword string `json:"new_password"`
}

type CreateTokenRequest struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

func (a *API) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.db.ListUsers()
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if users == nil {
		users = []db.User{}
	}
	a.jsonResponse(w, http.StatusOK, users)
}

func (a *API) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		a.jsonError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if req.Role == "" {
		req.Role = auth.RoleViewer
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user := &db.User{
		ID:           uuid.New().String(),
		Username:     req.Username,
		PasswordHash: hash,
		DisplayName:  req.DisplayName,
		Email:        req.Email,
		Role:         req.Role,
		AuthSource:   "local",
		IsActive:     true,
	}

	if err := a.db.CreateUser(user); err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResponse(w, http.StatusCreated, user)
}

func (a *API) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var user db.User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user.ID = id

	if err := a.db.UpdateUser(&user); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, user)
}

func (a *API) handleUpdateUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req UpdatePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewPassword == "" {
		a.jsonError(w, http.StatusBadRequest, "new_password is required")
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	if err := a.db.UpdateUserPassword(id, hash); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "password updated"})
}

func (a *API) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.db.DeleteUser(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "user deleted"})
}

func (a *API) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := a.db.ListAPITokens()
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tokens == nil {
		tokens = []db.APIToken{}
	}
	a.jsonResponse(w, http.StatusOK, tokens)
}

func (a *API) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		a.jsonError(w, http.StatusBadRequest, "token name is required")
		return
	}

	rawToken, err := auth.GenerateRandomPassword(32)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	fullToken := fmt.Sprintf("jobcon_%s", rawToken)
	hash := sha256.Sum256([]byte(fullToken))
	tokenHash := hex.EncodeToString(hash[:])

	token := &db.APIToken{
		ID:        uuid.New().String(),
		Name:      req.Name,
		TokenHash: tokenHash,
		Role:      req.Role,
	}

	if err := a.db.CreateAPIToken(token); err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return raw token once to user
	a.jsonResponse(w, http.StatusCreated, map[string]any{
		"token": token,
		"raw":   fullToken,
	})
}

func (a *API) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.db.DeleteAPIToken(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "token not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "token deleted"})
}

func (a *API) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.db.GetAllSettings()
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, settings)
}

func (a *API) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var settings map[string]string
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	for k, v := range settings {
		if err := a.db.SetSetting(k, v); err != nil {
			a.jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "settings saved"})
}
