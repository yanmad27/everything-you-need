package usermanagement

import (
	"errors"
	"sync"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserAlreadyExists = errors.New("user already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// UserStorage defines the interface for user storage operations
type UserStorage interface {
	Create(user *User) error
	GetByID(id string) (*User, error)
	GetByUsername(username string) (*User, error)
	GetByEmail(email string) (*User, error)
	GetAll() ([]*User, error)
	Update(user *User) error
	Delete(id string) error
}

// InMemoryUserStorage implements UserStorage using in-memory storage
type InMemoryUserStorage struct {
	users map[string]*User // key: user ID
	mu    sync.RWMutex
}

// NewInMemoryUserStorage creates a new in-memory user storage
func NewInMemoryUserStorage() *InMemoryUserStorage {
	return &InMemoryUserStorage{
		users: make(map[string]*User),
	}
}

// Create adds a new user to the storage
func (s *InMemoryUserStorage) Create(user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if user with same ID already exists
	if _, exists := s.users[user.ID]; exists {
		return ErrUserAlreadyExists
	}

	// Check if username already exists
	for _, u := range s.users {
		if u.Username == user.Username {
			return ErrUserAlreadyExists
		}
		if u.Email == user.Email {
			return ErrUserAlreadyExists
		}
	}

	s.users[user.ID] = user
	return nil
}

// GetByID retrieves a user by ID
func (s *InMemoryUserStorage) GetByID(id string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[id]
	if !exists {
		return nil, ErrUserNotFound
	}

	return user, nil
}

// GetByUsername retrieves a user by username
func (s *InMemoryUserStorage) GetByUsername(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, user := range s.users {
		if user.Username == username {
			return user, nil
		}
	}

	return nil, ErrUserNotFound
}

// GetByEmail retrieves a user by email
func (s *InMemoryUserStorage) GetByEmail(email string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, user := range s.users {
		if user.Email == email {
			return user, nil
		}
	}

	return nil, ErrUserNotFound
}

// GetAll retrieves all users
func (s *InMemoryUserStorage) GetAll() ([]*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]*User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, user)
	}

	return users, nil
}

// Update updates an existing user
func (s *InMemoryUserStorage) Update(user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[user.ID]; !exists {
		return ErrUserNotFound
	}

	// Check if new username/email conflicts with other users
	for _, u := range s.users {
		if u.ID != user.ID {
			if u.Username == user.Username {
				return ErrUserAlreadyExists
			}
			if u.Email == user.Email {
				return ErrUserAlreadyExists
			}
		}
	}

	s.users[user.ID] = user
	return nil
}

// Delete removes a user from storage
func (s *InMemoryUserStorage) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[id]; !exists {
		return ErrUserNotFound
	}

	delete(s.users, id)
	return nil
}
