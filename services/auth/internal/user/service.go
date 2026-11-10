package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrValidation         = errors.New("validation failed")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
)

// Service holds the auth business logic, independent of HTTP and SQL.
type Service struct {
	repo   Repository
	tokens *TokenManager
	log    *slog.Logger
}

func NewService(repo Repository, tokens *TokenManager, log *slog.Logger) *Service {
	return &Service{repo: repo, tokens: tokens, log: log}
}

type RegisterInput struct {
	Name     string
	Email    string
	Password string
	Role     Role
}

// Register validates the input, rejects duplicate emails, hashes the
// password and stores the new user.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*User, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Role == "" {
		in.Role = RoleCustomer
	}
	if err := validateRegister(in); err != nil {
		return nil, err
	}

	if _, err := s.repo.ByEmail(ctx, in.Email); err == nil {
		return nil, ErrEmailTaken
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &User{Name: in.Name, Email: in.Email, PasswordHash: string(hash), Role: in.Role}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	s.log.Info("user registered", "user_id", u.ID, "role", u.Role)
	return u, nil
}

// Login checks the credentials and returns a signed JWT plus the user.
func (s *Service) Login(ctx context.Context, email, password string) (string, *User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, err := s.repo.ByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return "", nil, ErrInvalidCredentials
	}
	token, err := s.tokens.Issue(u)
	if err != nil {
		return "", nil, err
	}
	return token, u, nil
}

// Me returns the user with the given ID.
func (s *Service) Me(ctx context.Context, id int64) (*User, error) {
	return s.repo.ByID(ctx, id)
}

// ParseToken verifies a JWT issued by this service.
func (s *Service) ParseToken(token string) (*Claims, error) {
	return s.tokens.Parse(token)
}

func validateRegister(in RegisterInput) error {
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	addr, err := mail.ParseAddress(in.Email)
	if err != nil || addr.Address != in.Email {
		return fmt.Errorf("%w: email is not valid", ErrValidation)
	}
	if len(in.Password) < 8 {
		return fmt.Errorf("%w: password must be at least 8 characters", ErrValidation)
	}
	if !in.Role.Valid() {
		return fmt.Errorf("%w: role must be customer, courier or admin", ErrValidation)
	}
	return nil
}
