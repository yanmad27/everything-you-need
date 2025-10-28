package usermanagement

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/submodule-org/submodule.go/v2"
)

// UserManagementService handles user management operations
type UserManagementService struct {
	storage UserStorage
}

// UserManagementServiceMod is the dependency injection module for UserManagementService
var UserManagementServiceMod = submodule.New(func() *UserManagementService {
	return NewUserManagementService(NewInMemoryUserStorage())
})

// NewUserManagementService creates a new user management service
func NewUserManagementService(storage UserStorage) *UserManagementService {
	return &UserManagementService{
		storage: storage,
	}
}

// CreateUser creates a new user
func (s *UserManagementService) CreateUser(req *UserCreateRequest) (*User, error) {
	// Validate input
	if req.Username == "" || req.Email == "" || req.Password == "" {
		return nil, errors.New("username, email, and password are required")
	}

	// Hash password
	passwordHash, err := HashPassword(req.Password)
	if err != nil {
		return nil, errors.New("failed to hash password")
	}

	// Set default role if not specified
	role := req.Role
	if role == "" {
		role = RoleUser
	}

	// Create user
	user := &User{
		ID:           uuid.New().String(),
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Store user
	if err := s.storage.Create(user); err != nil {
		return nil, err
	}

	return user, nil
}

// GetUserByID retrieves a user by ID
func (s *UserManagementService) GetUserByID(id string) (*User, error) {
	return s.storage.GetByID(id)
}

// GetUserByUsername retrieves a user by username
func (s *UserManagementService) GetUserByUsername(username string) (*User, error) {
	return s.storage.GetByUsername(username)
}

// GetAllUsers retrieves all users
func (s *UserManagementService) GetAllUsers() ([]*User, error) {
	return s.storage.GetAll()
}

// UpdateUser updates an existing user
func (s *UserManagementService) UpdateUser(id string, req *UserUpdateRequest) (*User, error) {
	// Get existing user
	user, err := s.storage.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Update fields if provided
	if req.Username != "" {
		user.Username = req.Username
	}
	if req.Email != "" {
		user.Email = req.Email
	}
	if req.Role != "" {
		user.Role = req.Role
	}

	user.UpdatedAt = time.Now()

	// Save updated user
	if err := s.storage.Update(user); err != nil {
		return nil, err
	}

	return user, nil
}

// DeleteUser deletes a user
func (s *UserManagementService) DeleteUser(id string) error {
	return s.storage.Delete(id)
}

// Login authenticates a user and returns the user if successful
func (s *UserManagementService) Login(req *LoginRequest) (*User, error) {
	// Find user by username
	user, err := s.storage.GetByUsername(req.Username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	// Check password
	if !CheckPassword(req.Password, user.PasswordHash) {
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

// ChangePassword changes a user's password
func (s *UserManagementService) ChangePassword(userID string, req *ChangePasswordRequest) error {
	// Get user
	user, err := s.storage.GetByID(userID)
	if err != nil {
		return err
	}

	// Verify old password
	if !CheckPassword(req.OldPassword, user.PasswordHash) {
		return errors.New("old password is incorrect")
	}

	// Hash new password
	newPasswordHash, err := HashPassword(req.NewPassword)
	if err != nil {
		return errors.New("failed to hash new password")
	}

	// Update password
	user.PasswordHash = newPasswordHash
	user.UpdatedAt = time.Now()

	return s.storage.Update(user)
}

// CreateDefaultAdmin creates a default admin user if no users exist
func (s *UserManagementService) CreateDefaultAdmin(username, email, password string) error {
	// Check if any users exist
	users, err := s.storage.GetAll()
	if err != nil {
		return err
	}

	// If users exist, don't create default admin
	if len(users) > 0 {
		return nil
	}

	// Create default admin
	_, err = s.CreateUser(&UserCreateRequest{
		Username: username,
		Email:    email,
		Password: password,
		Role:     RoleAdmin,
	})

	return err
}
