package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"jobcon/internal/auth"
	"jobcon/internal/db"
	"jobcon/internal/domain"
	"jobcon/internal/services"
	"net/http"

	"github.com/google/uuid"
)

type CreateUserRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	AuthSource  string `json:"auth_source"`
}

type UpdatePasswordRequest struct {
	NewPassword string `json:"new_password"`
}

type CreateTokenRequest struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

func (a *API) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.userSvc.ListUsers()
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if users == nil {
		users = []domain.User{}
	}
	a.jsonResponse(w, http.StatusOK, users)
}

func (a *API) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var cmd services.CreateUserCommand
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := a.userSvc.CreateUser(r.Context(), cmd)
	if err != nil {
		a.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	a.jsonResponse(w, http.StatusCreated, user)
}

func (a *API) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var cmd services.UpdateUserCommand
	cmd.ID = r.PathValue("id")
	if err := json.NewDecoder(r.Body).Decode(&cmd.CreateUserCommand); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := a.userSvc.UpdateUser(r.Context(), cmd); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		if errors.Is(err, db.ErrLastAdminProtection) {
			a.jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "user updated"})
}

func (a *API) handleUpdateUserPassword(w http.ResponseWriter, r *http.Request) {
	var cmd services.UpdatePasswordCommand
	cmd.UserID = r.PathValue("id")

	// API expects {"new_password": "..."} but DTO expects Password. Let's map it.
	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cmd.Password = req.NewPassword

	if err := a.userSvc.UpdatePassword(r.Context(), cmd); err != nil {
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
	if err := a.userSvc.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "user not found")
			return
		}
		if errors.Is(err, db.ErrLastAdminProtection) {
			a.jsonError(w, http.StatusBadRequest, err.Error())
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
		tokens = []domain.APIToken{}
	}
	a.jsonResponse(w, http.StatusOK, tokens)
}

func (a *API) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		a.jsonError(w, http.StatusBadRequest, "token name is required")
		return
	}

	fullToken, err := auth.GenerateAPIToken(40)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	hash := sha256.Sum256([]byte(fullToken))
	tokenHash := hex.EncodeToString(hash[:])

	token := &domain.APIToken{
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
