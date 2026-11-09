package identity

import (
	"context"
)

// Access modes describe which task owners an authenticated user may access.
type AccessMode string

const (
	AccessAll          = "All"
	AccessOwner        = "Owner"
	AccessOwnerOrAdmin = "OwnerOrAdmin"
)

// UserInfo describes the authenticated caller and the task access policy
// selected by server configuration.
type UserInfo struct {
	// Public users are unauthenticated callers, including callers of public endpoints.
	IsPublic bool
	// Administrators are defined by the configuration file:
	// Basic-auth users with Admin set, or usernames listed under OIDC Admins.
	IsAdmin bool
	// Username of an authenticated user (the subject field from a JWT for OIDC).
	Username string
	// OIDC bearer token, used when requesting task input data.
	Token string
	// AccessMode is assigned by server authentication after validating config.
	AccessMode AccessMode
}

type userInfoContextKey struct{}

var systemUserInfo = UserInfo{IsAdmin: true, Username: "SYSTEM"}

// WithUser returns a context carrying user identity and task access policy.
func WithUser(ctx context.Context, user *UserInfo) context.Context {
	return context.WithValue(ctx, userInfoContextKey{}, user)
}

// GetUser returns the user attached to ctx, or the internal SYSTEM identity
// when no user is attached.
func GetUser(ctx context.Context) *UserInfo {
	if userInfo, ok := ctx.Value(userInfoContextKey{}).(*UserInfo); ok {
		return userInfo
	}
	return &systemUserInfo
}

// GetUsername returns the attached user name, or SYSTEM for internal requests.
func GetUsername(ctx context.Context) string {
	return GetUser(ctx).Username
}

// IsAccessible reports whether the user can access data owned by dataOwner.
func (u *UserInfo) IsAccessible(dataOwner string) bool {
	if *u == systemUserInfo || u.AccessMode == "" || u.AccessMode == AccessAll {
		return true
	}

	isOwner := u != nil && u.Username == dataOwner
	if u.AccessMode == AccessOwner {
		return isOwner
	}

	if u.AccessMode == AccessOwnerOrAdmin {
		return isOwner || u != nil && u.IsAdmin
	}

	return false
}

// CanSeeAllTasks reports whether the user can list all tasks without checking
// each task's owner.
func (u *UserInfo) CanSeeAllTasks() bool {
	return *u == systemUserInfo ||
		u.AccessMode == "" || u.AccessMode == AccessAll ||
		u.AccessMode == AccessOwnerOrAdmin && u != nil && u.IsAdmin
}

func GetUserID(ctx context.Context) string {
	if username := GetUsername(ctx); username != "" {
		return username
	}
	return "ANONYMOUS"
}
