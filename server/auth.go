package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/ohsu-comp-bio/funnel/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Authentication struct {
	admins     map[string]bool
	basic      map[string]string
	oidc       *OidcConfig
	accessMode string
}

const (
	AccessAll          = "All"
	AccessOwner        = "Owner"
	AccessOwnerOrAdmin = "OwnerOrAdmin"
)

// Extracted info about the current user, which is exposed through Context.
type UserInfo struct {
	// Public users are non-authenticated, in case Funnel configuration does
	// not require OIDC nor Basic authentication.
	IsPublic bool
	// Administrators are defined by the configuration file:
	// 1) Basic-authentication: a user with the `Admin: true` property.
	// 2) OIDC-authentication: one of the usernames under the `Admins` property.
	IsAdmin bool
	// Username of an authenticated user (subject field from JWT).
	Username string
	// In case of OIDC authentication, the provided Bearer token, which can be
	// used when requesting task input data.
	Token      string
	accessMode string
}

// Context key type for storing UserInfo.
// Note: UserInfo is not in the context when the system internally requests data.
type userInfoContextKey string

var (
	errMissingMetadata    = status.Errorf(codes.InvalidArgument, "Missing metadata in the context")
	errTokenRequired      = status.Errorf(codes.Unauthenticated, "Basic/Bearer authorization token missing")
	errInvalidBasicToken  = status.Errorf(codes.Unauthenticated, "Basic-authentication failed")
	errInvalidBearerToken = status.Errorf(codes.Unauthenticated, "Bearer authorization token not accepted")
	publicUserInfo        = UserInfo{IsPublic: true, IsAdmin: false, Username: ""}
	systemUserInfo        = UserInfo{IsPublic: false, IsAdmin: true, Username: "SYSTEM"}
	UserInfoKey           = userInfoContextKey("user-info")
)

func GetUser(ctx context.Context) *UserInfo {
	if userInfo, ok := ctx.Value(UserInfoKey).(*UserInfo); ok {
		return userInfo
	}
	return &systemUserInfo
}

func GetUsername(ctx context.Context) string {
	return GetUser(ctx).Username
}

func NewAuthentication(
	creds []*config.BasicCredential,
	oidc *config.OidcAuth,
	taskAccess string,
) *Authentication {
	basicCreds := make(map[string]string)
	adminUsers := make(map[string]bool)

	for _, cred := range creds {
		credBytes := []byte(cred.User + ":" + cred.Password)
		fullValue := "Basic " + base64.StdEncoding.EncodeToString(credBytes)
		basicCreds[fullValue] = cred.User
		if cred.Admin {
			adminUsers[cred.User] = true
		}
	}

	switch taskAccess {
	case "":
		taskAccess = AccessAll
	case AccessAll, AccessOwner, AccessOwnerOrAdmin:
	default:
		fmt.Printf("[ERROR] Bad configuration value for Server.TaskAccess (%s). "+
			"Expected 'All', 'Owner', or 'OwnerOrAdmin'.\n", taskAccess)
		os.Exit(1)
	}

	return &Authentication{
		admins:     adminUsers,
		basic:      basicCreds,
		oidc:       initOidcConfig(oidc),
		accessMode: taskAccess,
	}
}

// Return a new gRPC interceptor function that authorizes RPCs.
func (a *Authentication) Interceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (interface{}, error) {

	// Case when authentication is not required:
	if len(a.basic) == 0 && a.oidc == nil {
		userInfo := publicUserInfo
		userInfo.accessMode = a.accessMode
		ctx = context.WithValue(ctx, UserInfoKey, &userInfo)
		return handler(ctx, req)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errMissingMetadata
	}

	values := md["authorization"]
	if len(values) == 0 {
		return nil, errTokenRequired
	}

	authorized := false
	authErr := errTokenRequired
	authorization := values[0]

	if strings.HasPrefix(authorization, "Basic ") {
		authErr = errInvalidBasicToken
		username := a.basic[authorization]
		authorized = username != ""

		if authorized {
			isAdmin := a.admins[username]
			ctx = context.WithValue(ctx, UserInfoKey,
				&UserInfo{Username: username, IsAdmin: isAdmin, accessMode: a.accessMode})
		}
	} else if a.oidc != nil && strings.HasPrefix(authorization, "Bearer ") {
		authErr = errInvalidBearerToken
		if userInfo := a.oidc.Authorize(authorization); userInfo != nil {
			userInfo.accessMode = a.accessMode
			ctx = context.WithValue(ctx, UserInfoKey, userInfo)
			authorized = true
		}
	}

	if !authorized {
		return nil, authErr
	}

	return handler(ctx, req)
}

// HTTP request handler for the /login endpoint. Initiates user authentication
// flow based on the configuration (OIDC, Basic, none).
func (a *Authentication) LoginHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Only GET method is supported.", http.StatusMethodNotAllowed)
	}

	if a.oidc != nil {
		a.oidc.HandleAuthCode(w, req)
	} else if len(a.basic) > 0 {
		a.handleBasicAuth(w, req)
	} else {
		http.Redirect(w, req, "/", http.StatusSeeOther)
	}
}

// HTTP request handler for the /login/token endpoint. In case of OIDC enabled,
// prints the JWT from the sent cookie. In all other cases, an empty HTTP 200
// response.
func (a *Authentication) EchoTokenHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Only GET method is supported.", http.StatusMethodNotAllowed)
	}

	if a.oidc != nil {
		a.oidc.EchoTokenHandler(w, req)
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}
}

func (a *Authentication) handleBasicAuth(w http.ResponseWriter, req *http.Request) {
	// Check if provided value in the header is valid:
	if a.basic[req.Header.Get("Authorization")] == "" {
		http.Redirect(w, req, "/", http.StatusSeeOther)
	} else {
		w.Header().Set("WWW-Authenticate", "Basic realm=Funnel")
		msg := "User authentication is required (Basic authentication with " +
			"username and password)"
		http.Error(w, msg, http.StatusUnauthorized)
	}
}

// Reports whether the current user can access data with the specified owner.
// Evaluation depends on configuration (Server.TaskAccess), current username,
// and the username recorded in the task. For public users and unknown task
// owners, the username is an empty string.
func (u *UserInfo) IsAccessible(dataOwner string) bool {
	if *u == systemUserInfo || u.accessMode == "" || u.accessMode == AccessAll {
		return true
	}

	isOwner := u != nil && u.Username == dataOwner
	if u.accessMode == AccessOwner {
		return isOwner
	}

	if u.accessMode == AccessOwnerOrAdmin {
		return isOwner || u != nil && u.IsAdmin
	}

	return false
}

// Reports whether the current user can access all tasks considering the
// configuration (Server.TaskAccess) and whether the user has Admin status.
// If the result is false, data access must be verified (see: IsAccessible).
func (u *UserInfo) CanSeeAllTasks() bool {
	return *u == systemUserInfo ||
		u.accessMode == "" || u.accessMode == AccessAll ||
		u.accessMode == AccessOwnerOrAdmin && u != nil && u.IsAdmin
}
