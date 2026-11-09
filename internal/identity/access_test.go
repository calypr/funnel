package identity

import (
	"context"
	"testing"
)

func TestAccessPolicies(t *testing.T) {
	for _, tt := range []struct {
		name      string
		user      *UserInfo
		owner     string
		own       bool
		other     bool
		canSeeAll bool
	}{
		{"system fallback", GetUser(context.Background()), "owner", true, true, true},
		{"all mode", &UserInfo{Username: "owner", AccessMode: AccessAll}, "owner", true, true, true},
		{"owner mode", &UserInfo{Username: "owner", AccessMode: AccessOwner}, "owner", true, false, false},
		{"admin under owner mode", &UserInfo{Username: "admin", IsAdmin: true, AccessMode: AccessOwner}, "owner", false, false, false},
		{"owner or admin owner", &UserInfo{Username: "owner", AccessMode: AccessOwnerOrAdmin}, "owner", true, false, false},
		{"owner or admin admin", &UserInfo{Username: "admin", IsAdmin: true, AccessMode: AccessOwnerOrAdmin}, "owner", true, true, true},
		{"public under owner mode", &UserInfo{IsPublic: true, AccessMode: AccessOwner}, "", true, false, false},
		{"unspecified mode", &UserInfo{Username: "owner"}, "owner", true, true, true},
		{"unknown mode", &UserInfo{Username: "owner", AccessMode: "Unknown"}, "owner", false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.user.IsAccessible(tt.owner); got != tt.own {
				t.Errorf("owner access = %v, want %v", got, tt.own)
			}
			if got := tt.user.IsAccessible("another-user"); got != tt.other {
				t.Errorf("other-owner access = %v, want %v", got, tt.other)
			}
			if got := tt.user.CanSeeAllTasks(); got != tt.canSeeAll {
				t.Errorf("can see all tasks = %v, want %v", got, tt.canSeeAll)
			}
		})
	}
}

func TestWithUserAndGetUser(t *testing.T) {
	user := &UserInfo{Username: "alice", AccessMode: AccessOwner}
	ctx := WithUser(context.Background(), user)
	if got := GetUser(ctx); got != user {
		t.Fatalf("GetUser() = %p, want same user %p", got, user)
	}
	if got := GetUsername(ctx); got != "alice" {
		t.Fatalf("GetUsername() = %q, want alice", got)
	}
	if got := GetUsername(context.Background()); got != "SYSTEM" {
		t.Fatalf("missing identity username = %q, want SYSTEM", got)
	}
}
