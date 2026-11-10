package user

import "time"

// Role is the kind of account a user holds on the platform.
type Role string

const (
	RoleCustomer Role = "customer"
	RoleCourier  Role = "courier"
	RoleAdmin    Role = "admin"
)

// Valid reports whether r is one of the known roles.
func (r Role) Valid() bool {
	switch r {
	case RoleCustomer, RoleCourier, RoleAdmin:
		return true
	}
	return false
}

// User is one platform account. PasswordHash never leaves the service.
type User struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}
