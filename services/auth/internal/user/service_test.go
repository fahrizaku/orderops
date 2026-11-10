package user

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeRepo is an in-memory Repository for unit tests.
type fakeRepo struct {
	mu      sync.Mutex
	byEmail map[string]*User
	nextID  int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byEmail: map[string]*User{}, nextID: 1}
}

func (f *fakeRepo) Create(_ context.Context, u *User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u.ID = f.nextID
	f.nextID++
	u.CreatedAt = time.Now()
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeRepo) ByEmail(_ context.Context, email string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byEmail[email]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

func (f *fakeRepo) ByID(_ context.Context, id int64) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func newTestService() *Service {
	return NewService(newFakeRepo(), NewTokenManager("test-secret", time.Hour), slog.Default())
}

func TestRegisterSuccess(t *testing.T) {
	svc := newTestService()
	u, err := svc.Register(context.Background(), RegisterInput{
		Name: "Ada", Email: "ADA@Example.com", Password: "supersecret",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if u.ID == 0 {
		t.Error("expected an assigned ID")
	}
	if u.Email != "ada@example.com" {
		t.Errorf("email not normalised: %q", u.Email)
	}
	if u.Role != RoleCustomer {
		t.Errorf("default role = %q, want customer", u.Role)
	}
	if u.PasswordHash == "" || u.PasswordHash == "supersecret" {
		t.Error("password must be stored hashed, never in plain text")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	svc := newTestService()
	in := RegisterInput{Name: "Ada", Email: "ada@example.com", Password: "supersecret"}
	if _, err := svc.Register(context.Background(), in); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := svc.Register(context.Background(), in); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("second register err = %v, want ErrEmailTaken", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	svc := newTestService()
	cases := map[string]RegisterInput{
		"empty name":    {Name: "", Email: "ada@example.com", Password: "supersecret"},
		"invalid email": {Name: "Ada", Email: "not-an-email", Password: "supersecret"},
		"short pass":    {Name: "Ada", Email: "ada@example.com", Password: "short"},
		"unknown role":  {Name: "Ada", Email: "ada@example.com", Password: "supersecret", Role: "superuser"},
	}
	for name, in := range cases {
		if _, err := svc.Register(context.Background(), in); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}

func TestLoginIssuesVerifiableToken(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	registered, err := svc.Register(ctx, RegisterInput{
		Name: "Bima", Email: "bima@example.com", Password: "supersecret", Role: RoleCourier,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	token, u, err := svc.Login(ctx, "bima@example.com", "supersecret")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u.ID != registered.ID {
		t.Errorf("login returned user %d, want %d", u.ID, registered.ID)
	}
	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("parse issued token: %v", err)
	}
	if claims.Subject != strconv.FormatInt(registered.ID, 10) {
		t.Errorf("token subject = %q, want user id %d", claims.Subject, registered.ID)
	}
	if claims.Role != RoleCourier {
		t.Errorf("token role = %q, want courier", claims.Role)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	if _, err := svc.Register(ctx, RegisterInput{
		Name: "Ada", Email: "ada@example.com", Password: "supersecret",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, err := svc.Login(ctx, "ada@example.com", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password err = %v, want ErrInvalidCredentials", err)
	}
	if _, _, err := svc.Login(ctx, "ghost@example.com", "supersecret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("unknown email err = %v, want ErrInvalidCredentials", err)
	}
}

func TestTokenFromAnotherSecretIsRejected(t *testing.T) {
	repo := newFakeRepo()
	issuer := NewService(repo, NewTokenManager("secret-a", time.Hour), slog.Default())
	verifier := NewService(repo, NewTokenManager("secret-b", time.Hour), slog.Default())
	ctx := context.Background()
	if _, err := issuer.Register(ctx, RegisterInput{
		Name: "Ada", Email: "ada@example.com", Password: "supersecret",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	token, _, err := issuer.Login(ctx, "ada@example.com", "supersecret")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := verifier.ParseToken(token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("parse with wrong secret err = %v, want ErrInvalidToken", err)
	}
}
