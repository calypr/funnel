package auth

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/internal/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestAuthenticationPoliciesAreIndependent(t *testing.T) {
	creds := []*config.BasicCredential{
		{User: "owner", Password: "password"},
		{User: "admin", Password: "password", Admin: true},
	}
	owner := NewAuthentication(creds, nil, identity.AccessOwner, nil)
	ownerOrAdmin := NewAuthentication(creds, nil, identity.AccessOwnerOrAdmin, nil)
	publicOwner := NewAuthentication(nil, nil, identity.AccessOwner, nil)
	all := NewAuthentication(creds, nil, identity.AccessAll, nil)
	publicAll := NewAuthentication(nil, nil, "", nil)

	for _, tt := range []struct {
		name       string
		auth       *Authentication
		username   string
		public     bool
		admin      bool
		accessMode identity.AccessMode
		own        bool
		other      bool
		seeAll     bool
	}{
		{"owner", owner, "owner", false, false, identity.AccessOwner, true, false, false},
		{"admin under owner policy", owner, "admin", false, true, identity.AccessOwner, true, false, false},
		{"owner under admin policy", ownerOrAdmin, "owner", false, false, identity.AccessOwnerOrAdmin, true, false, false},
		{"admin", ownerOrAdmin, "admin", false, true, identity.AccessOwnerOrAdmin, true, true, true},
		{"public owner", publicOwner, "", true, false, identity.AccessOwner, true, false, false},
		{"all", all, "owner", false, false, identity.AccessAll, true, true, true},
		{"public default", publicAll, "", true, false, identity.AccessAll, true, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.username != "" {
				token := base64.StdEncoding.EncodeToString([]byte(tt.username + ":password"))
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Basic "+token))
			}
			_, err := tt.auth.Interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, _ interface{}) (interface{}, error) {
				user := identity.GetUser(ctx)
				if user.Username != tt.username || user.IsPublic != tt.public || user.IsAdmin != tt.admin || user.AccessMode != tt.accessMode {
					t.Errorf("authenticated identity = %+v", user)
				}
				if got := user.IsAccessible(tt.username); got != tt.own {
					t.Errorf("own task access = %v, want %v", got, tt.own)
				}
				if got := user.IsAccessible("another-user"); got != tt.other {
					t.Errorf("another user's task access = %v, want %v", got, tt.other)
				}
				if got := user.CanSeeAllTasks(); got != tt.seeAll {
					t.Errorf("all tasks access = %v, want %v", got, tt.seeAll)
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	if user := identity.GetUser(context.Background()); user.Username != "SYSTEM" || !user.IsAdmin ||
		!user.IsAccessible("another-user") || !user.CanSeeAllTasks() {
		t.Fatalf("internal requests should use SYSTEM identity, got %+v", user)
	}
}
