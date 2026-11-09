package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/calypr/funnel/api/config"
	"github.com/calypr/funnel/internal/identity"
	"github.com/calypr/funnel/internal/logger"
	"github.com/calypr/funnel/internal/tesrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Authentication struct {
	admins     map[string]bool
	basic      map[string]string
	oidc       *OidcConfig
	accessMode identity.AccessMode
	log        *logger.Logger
}

var (
	errMissingMetadata    = status.Errorf(codes.InvalidArgument, "Missing metadata in the context")
	errTokenRequired      = status.Errorf(codes.Unauthenticated, "Basic/Bearer authorization token missing")
	errInvalidBasicToken  = status.Errorf(codes.Unauthenticated, "Basic-authentication failed")
	errInvalidBearerToken = status.Errorf(codes.Unauthenticated, "Bearer authorization token not accepted")
)

func NewAuthentication(
	creds []*config.BasicCredential,
	oidc *config.OidcAuth,
	taskAccess string,
	log *logger.Logger,
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
		taskAccess = identity.AccessAll
	case identity.AccessAll, identity.AccessOwner, identity.AccessOwnerOrAdmin:
	default:
		fmt.Printf("[ERROR] Bad configuration value for Server.TaskAccess (%s). "+
			"Expected 'All', 'Owner', or 'OwnerOrAdmin'.\n", taskAccess)
		os.Exit(1)
	}

	return &Authentication{
		admins:     adminUsers,
		basic:      basicCreds,
		oidc:       initOidcConfig(oidc),
		accessMode: identity.AccessMode(taskAccess),
		log:        log,
	}
}

// Return a new gRPC interceptor function that authorizes RPCs.
func (a *Authentication) Interceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (interface{}, error) {

	if (len(a.basic) == 0 && a.oidc == nil) || info.FullMethod == tesrpc.TaskService_GetServiceInfo_FullMethodName {
		userInfo := identity.UserInfo{IsPublic: true, AccessMode: a.accessMode}
		ctx = identity.WithUser(ctx, &userInfo)
		return handler(ctx, req)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		logAuditEntry(a.log, info.FullMethod, anonymousUserID, "", errMissingMetadata)
		return nil, errMissingMetadata
	}

	values := md["authorization"]
	if len(values) == 0 {
		logAuditEntry(a.log, info.FullMethod, anonymousUserID, "", errTokenRequired)
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
			ctx = identity.WithUser(ctx,
				&identity.UserInfo{Username: username, IsAdmin: isAdmin, AccessMode: a.accessMode})
		}
	} else if a.oidc != nil && strings.HasPrefix(authorization, "Bearer ") {
		authErr = errInvalidBearerToken
		if userInfo := a.oidc.Authorize(authorization); userInfo != nil {
			userInfo.AccessMode = a.accessMode
			ctx = identity.WithUser(ctx, userInfo)
			authorized = true
		}
	}

	if !authorized {
		logAuditEntry(a.log, info.FullMethod, anonymousUserID, "", authErr)
		return nil, authErr
	}
	switch info.FullMethod {
	case "/scheduler.SchedulerService/PutNode", "/scheduler.SchedulerService/DeleteNode":
		if !identity.GetUser(ctx).IsAdmin {
			err := status.Error(codes.PermissionDenied, "scheduler node management requires administrator credentials")
			logAuditEntry(a.log, info.FullMethod, identity.GetUserID(ctx), "", err)
			return nil, err
		}
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
	if a.basic[req.Header.Get("Authorization")] != "" {
		http.Redirect(w, req, "/", http.StatusSeeOther)
	} else {
		w.Header().Set("WWW-Authenticate", "Basic realm=Funnel")
		msg := "User authentication is required (Basic authentication with " +
			"username and password)"
		http.Error(w, msg, http.StatusUnauthorized)
	}
}
