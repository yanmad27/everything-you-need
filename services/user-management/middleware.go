package usermanagement

import (
	"context"
	"net/http"
	"strings"
)

// requireAuth is a middleware that requires authentication
func (h *UserHandler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Authorization header required"})
			return
		}

		// Check if it's a Bearer token
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid authorization header format"})
			return
		}

		token := parts[1]

		// Validate token
		claims, err := h.jwtManager.ValidateToken(token)
		if err != nil {
			if err == ErrExpiredToken {
				respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Token has expired"})
			} else {
				respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid token"})
			}
			return
		}

		// Add claims to context
		ctx := context.WithValue(r.Context(), "claims", claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// RequireRole is a middleware that requires a specific role
func (h *UserHandler) RequireRole(role UserRole) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return h.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			claims := r.Context().Value("claims").(*JWTClaims)
			if claims.Role != role {
				respondJSON(w, http.StatusForbidden, map[string]string{"error": "Insufficient permissions"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
