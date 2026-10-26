package server

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/ohsu-comp-bio/funnel/config"
	"google.golang.org/grpc/metadata"
)

func TestAuthenticationPoliciesAreIndependent(t *testing.T) {
	creds := []*config.BasicCredential{
		{User: "owner", Password: "password"},
		{User: "admin", Password: "password", Admin: true},
	}
	owner := NewAuthentication(creds, nil, AccessOwner)
	ownerOrAdmin := NewAuthentication(creds, nil, AccessOwnerOrAdmin)
	publicOwner := NewAuthentication(nil, nil, AccessOwner)
	all := NewAuthentication(creds, nil, AccessAll)
	publicAll := NewAuthentication(nil, nil, "")

	for _, tt := range []struct {
		name     string
		auth     *Authentication
		username string
		own      bool
		other    bool
		seeAll   bool
	}{
		{"owner", owner, "owner", true, false, false},
		{"admin under owner policy", owner, "admin", true, false, false},
		{"owner under admin policy", ownerOrAdmin, "owner", true, false, false},
		{"admin", ownerOrAdmin, "admin", true, true, true},
		{"public owner", publicOwner, "", true, false, false},
		{"all", all, "owner", true, true, true},
		{"public default", publicAll, "", true, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.username != "" {
				token := base64.StdEncoding.EncodeToString([]byte(tt.username + ":password"))
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Basic "+token))
			}
			_, err := tt.auth.Interceptor(ctx, nil, nil, func(ctx context.Context, _ interface{}) (interface{}, error) {
				user := GetUser(ctx)
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
	if user := GetUser(context.Background()); !user.IsAccessible("another-user") || !user.CanSeeAllTasks() {
		t.Fatal("internal requests must retain access to all tasks")
	}
}
