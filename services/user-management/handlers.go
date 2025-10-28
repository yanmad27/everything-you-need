package usermanagement

import (
	"encoding/json"
	"net/http"
	"strings"
)

// UserHandler handles HTTP requests for user management
type UserHandler struct {
	service    *UserManagementService
	jwtManager *JWTManager
}

// NewUserHandler creates a new user handler
func NewUserHandler(service *UserManagementService, jwtManager *JWTManager) *UserHandler {
	return &UserHandler{
		service:    service,
		jwtManager: jwtManager,
	}
}

// RegisterRoutes registers all user management routes
func (h *UserHandler) RegisterRoutes(mux *http.ServeMux) {
	// Public routes
	mux.HandleFunc("/api/users/register", h.Register)
	mux.HandleFunc("/api/users/login", h.Login)

	// Protected routes
	mux.HandleFunc("/api/users", h.requireAuth(h.ListUsers))
	mux.HandleFunc("/api/users/", h.requireAuth(h.HandleUserByID))
	mux.HandleFunc("/api/users/change-password", h.requireAuth(h.ChangePassword))
}

// Register handles user registration
func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req UserCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	user, err := h.service.CreateUser(&req)
	if err != nil {
		if err == ErrUserAlreadyExists {
			respondJSON(w, http.StatusConflict, map[string]string{"error": "User already exists"})
		} else {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}

	respondJSON(w, http.StatusCreated, user.ToUserResponse())
}

// Login handles user login
func (h *UserHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	user, err := h.service.Login(&req)
	if err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid credentials"})
		return
	}

	token, err := h.jwtManager.GenerateToken(user)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
		return
	}

	respondJSON(w, http.StatusOK, LoginResponse{
		Token: token,
		User:  user,
	})
}

// ListUsers handles listing all users (admin only)
func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	// Check if user is admin
	claims := r.Context().Value("claims").(*JWTClaims)
	if claims.Role != RoleAdmin {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "Forbidden"})
		return
	}

	users, err := h.service.GetAllUsers()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch users"})
		return
	}

	// Convert to UserResponse
	userResponses := make([]*UserResponse, len(users))
	for i, user := range users {
		userResponses[i] = user.ToUserResponse()
	}

	respondJSON(w, http.StatusOK, userResponses)
}

// HandleUserByID handles operations on a specific user
func (h *UserHandler) HandleUserByID(w http.ResponseWriter, r *http.Request) {
	// Extract user ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	userID := strings.TrimSuffix(path, "/")

	if userID == "" || userID == "change-password" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "User ID required"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.GetUser(w, r, userID)
	case http.MethodPut:
		h.UpdateUser(w, r, userID)
	case http.MethodDelete:
		h.DeleteUser(w, r, userID)
	default:
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// GetUser handles getting a specific user
func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request, userID string) {
	user, err := h.service.GetUserByID(userID)
	if err != nil {
		if err == ErrUserNotFound {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "User not found"})
		} else {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch user"})
		}
		return
	}

	respondJSON(w, http.StatusOK, user.ToUserResponse())
}

// UpdateUser handles updating a user
func (h *UserHandler) UpdateUser(w http.ResponseWriter, r *http.Request, userID string) {
	// Check authorization
	claims := r.Context().Value("claims").(*JWTClaims)
	if claims.Role != RoleAdmin && claims.UserID != userID {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "Forbidden"})
		return
	}

	var req UserUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	// Non-admin users can't change their role
	if claims.Role != RoleAdmin && req.Role != "" {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "Cannot change role"})
		return
	}

	user, err := h.service.UpdateUser(userID, &req)
	if err != nil {
		if err == ErrUserNotFound {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "User not found"})
		} else if err == ErrUserAlreadyExists {
			respondJSON(w, http.StatusConflict, map[string]string{"error": "Username or email already exists"})
		} else {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}

	respondJSON(w, http.StatusOK, user.ToUserResponse())
}

// DeleteUser handles deleting a user
func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	// Only admins can delete users
	claims := r.Context().Value("claims").(*JWTClaims)
	if claims.Role != RoleAdmin {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "Forbidden"})
		return
	}

	if err := h.service.DeleteUser(userID); err != nil {
		if err == ErrUserNotFound {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "User not found"})
		} else {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete user"})
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "User deleted successfully"})
}

// ChangePassword handles password change
func (h *UserHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	claims := r.Context().Value("claims").(*JWTClaims)

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if err := h.service.ChangePassword(claims.UserID, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Password changed successfully"})
}

// respondJSON sends a JSON response
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
