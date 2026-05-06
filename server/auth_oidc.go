package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/ohsu-comp-bio/funnel/config"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
)

// JSON structure of the OIDC configuration (only some fields)
type OidcRemoteConfig struct {
	Issuer                string `json:"issuer"`
	JwksURI               string `json:"jwks_uri"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	IntrospectionEndpoint string `json:"introspection_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

// JSON structure of the OIDC token introspection response (only some fields)
type IntrospectionResponse struct {
	Active bool `json:"active"`
}

// OIDC configuration structure used for validating input from request.
type OidcConfig struct {
	local  *config.OidcAuth
	admins map[string]bool
	remote OidcRemoteConfig
	oauth2 *oauth2.Config
	jwks   jwk.Cache
}

func initOidcConfig(config *config.OidcAuth) *OidcConfig {
	if config == nil || config.ServiceConfigURL == "" {
		return nil
	} else if config.ClientId == "" {
		fmt.Printf("[ERROR] Missing configuration value [Server.OidcAuth.ClientId]")
		os.Exit(1)
	} else if config.ClientSecret == "" {
		fmt.Printf("[ERROR] Missing configuration value [Server.OidcAuth.ClientSecret]")
		os.Exit(1)
	} else if config.RedirectURL == "" {
		fmt.Printf("[ERROR] Missing configuration value [Server.OidcAuth.RedirectURL]")
		os.Exit(1)
	} else if !strings.HasSuffix(config.RedirectURL, "/login") {
		fmt.Printf("[ERROR] Configuration value [Server.OidcAuth.RedirectURL] must end with '/login'.")
		os.Exit(1)
	}

	result := OidcConfig{local: config}
	result.initConfig()
	return &result
}

func (c *OidcConfig) initConfig() {
	c.remote = OidcRemoteConfig{}
	parsedUrl := validateUrl(c.local.ServiceConfigURL)
	err := json.Unmarshal(fetchJson(parsedUrl), &c.remote)
	if err != nil {
		fmt.Printf("[ERROR] Failed to parse the configuration (JSON) of the "+
			"OIDC service: %s\n", err)
		os.Exit(1)
	}

	c.initJwks()

	c.oauth2.ClientID = c.local.ClientId
	c.oauth2.ClientSecret = c.local.ClientSecret
	if c.local.RequireScope == "" {
		c.oauth2.Scopes = []string{"openid"}
	} else {
		c.oauth2.Scopes = []string{"openid", c.local.RequireScope}
	}
	c.oauth2.RedirectURL = c.local.RedirectURL
	c.oauth2.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	c.oauth2.Endpoint.AuthURL = c.remote.AuthorizationEndpoint
	c.oauth2.Endpoint.TokenURL = c.remote.TokenEndpoint

	c.admins = map[string]bool{}
	for _, username := range c.local.Admins {
		c.admins[username] = true
	}
}

func (c *OidcConfig) initJwks() {
	jwksUrl := c.remote.JwksURI
	ctx := context.Background()

	// Define JWKS cache:
	c.jwks = *jwk.NewCache(ctx)
	if err := c.jwks.Register(jwksUrl, jwk.WithMinRefreshInterval(15*time.Minute)); err != nil {
		fmt.Printf("[ERROR] Failed to register JWKS (%s) of the OIDC service "+
			"(%s): %s\n", jwksUrl, c.local.ServiceConfigURL, err)
		os.Exit(1)
	}

	// Init JWKS cache:
	ctx2, _ := context.WithTimeout(ctx, 10*time.Second)
	_, err := c.jwks.Refresh(ctx2, jwksUrl)

	if err != nil {
		fmt.Printf("[ERROR] Failed to fetch JWKS (%s) of the OIDC service "+
			"(%s): %s\n", jwksUrl, c.local.ServiceConfigURL, err)
		os.Exit(1)
	}
}

func (c *OidcConfig) RedirectToLogin(w http.ResponseWriter, req *http.Request) {
	authCodeURL := c.oauth2.AuthCodeURL(c.computeState(req))
	http.Redirect(w, req, authCodeURL, http.StatusSeeOther)
}

func (c *OidcConfig) HandleAuthCode(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Only GET method is supported.", http.StatusMethodNotAllowed)
		return
	}

	state := req.FormValue("state")
	if state == "" {
		c.RedirectToLogin(w, req)
		return
	} else if state != c.computeState(req) {
		msg := "Unexpected value in the 'state' query-parameter."
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	errorCode := req.FormValue("error")
	errorDesc := req.FormValue("error_description")
	if errorCode != "" && errorDesc != "" {
		msg := "OIDC authentication flow failed [" + errorCode + "]: " + errorDesc
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	code := req.FormValue("code")
	if code == "" {
		c.RedirectToLogin(w, req)
		return
	}

	token, err := c.oauth2.Exchange(context.Background(), code)
	if err != nil {
		msg := "Failed to receive a JWT for the authorization code: " + err.Error()
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	cookie := http.Cookie{}
	cookie.Name = "jwt"
	cookie.Value = token.AccessToken
	cookie.Expires = token.Expiry
	cookie.SameSite = http.SameSiteStrictMode
	cookie.HttpOnly = true
	cookie.Secure = req.TLS != nil

	http.SetCookie(w, &cookie)
	http.Redirect(w, req, "/", http.StatusTemporaryRedirect)
}

// Prints the JWT from the cookie value in the response body. Missing cookie
// value results in HTTP 404 response.
// Frontend uses this endpoint for fetching the JWT for performing API requests.
// Frontend cannot access the cookie directly as it is HttpOnly.
// Although the API could also obtain the JWT from the cookie, it would be
// harder to maintain, especially in the gRPC code. Therefore, the frontend
// uses this login-token endpoint to establish JWT first, and then provide the
// JWT value in the Authorization header of API requests.
func (c *OidcConfig) EchoTokenHandler(w http.ResponseWriter, req *http.Request) {
	cookie, err := req.Cookie("jwt")
	if err != nil || len(cookie.Value) == 0 {
		msg := "Missing 'jwt' cookie. Please log in first."
		http.Error(w, msg, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(cookie.Value)))
	w.WriteHeader(http.StatusOK)

	if _, err := io.WriteString(w, cookie.Value); err != nil {
		fmt.Println("[WARN] Failed to write a JWT cookie value to HTTP response:", err)
	}
}

func (c *OidcConfig) computeState(req *http.Request) string {
	str := c.local.ServiceConfigURL + ":" +
		c.local.ClientId + ":" +
		c.local.ClientSecret + ":" +
		req.Header.Get("User-Agent")

	hash := sha256.New()
	hash.Write([]byte(str))
	b := hash.Sum(nil)
	return hex.EncodeToString(b)[:10]
}

func (c *OidcConfig) Authorize(authorization string) *UserInfo {
	jwtString := strings.TrimPrefix(authorization, "Bearer ")
	subject := c.ParseJwtSubject(jwtString)
	isAdmin := c.admins[subject]

	if subject == "" {
		return nil
	}
	return &UserInfo{Username: subject, Token: jwtString, IsAdmin: isAdmin}
}

func (c *OidcConfig) ParseJwtSubject(jwtString string) string {
	keySet, err := c.jwks.Get(context.Background(), c.remote.JwksURI)
	if err != nil {
		fmt.Printf("[WARN] Failed to retrieve JWKS key-set: %s", err)
		return ""
	}

	token, err := jwt.ParseString(
		jwtString,
		jwt.WithVerify(true),
		jwt.WithKeySet(keySet),
		jwt.WithIssuer(c.remote.Issuer),
	)

	if err != nil {
		fmt.Printf("[WARN] Provided JWT is not valid: %s.\n", err)
		return ""
	}

	if !c.isJwtValid(&token) || !c.isJwtActive(jwtString) {
		return ""
	}

	return token.Subject()
}

func (c *OidcConfig) isJwtValid(token *jwt.Token) bool {
	// If audience is required, it must be in the token.
	if c.local.RequireAudience != "" {
		found := false
		for _, value := range (*token).Audience() {
			if value == c.local.RequireAudience {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("[WARN] Audience [%s] not found in %v.",
				c.local.RequireAudience, (*token).Audience())
			return false
		}
	}

	// If scope is required, it must be in the token.
	if c.local.RequireScope != "" {
		value, found := (*token).Get("scope")
		if found {
			found = false
			for _, value := range strings.Split(value.(string), " ") {
				if value == c.local.RequireScope {
					found = true
					break
				}
			}
		}
		if !found {
			fmt.Printf("[WARN] Scope [%s] not found in [%s]",
				c.local.RequireScope, value)
			return false
		}
	}

	return true
}

func (c *OidcConfig) isJwtActive(token string) bool {
	if c.remote.IntrospectionEndpoint == "" {
		fmt.Println("[WARN] JWT introspection endpoint was not defined in the OIDC " +
			"(remote) configuration; therefore assuming that the token is active.")
		return true
	}

	client := &http.Client{}
	params := url.Values{"token": {token}}.Encode()
	attemptsCount := 3

	for attemptsCount > 0 {
		request, err := http.NewRequest(
			http.MethodPost,
			c.remote.IntrospectionEndpoint,
			strings.NewReader(params))

		if err != nil {
			fmt.Printf("[ERROR] Failed to create a new request for the OIDC "+
				"introspection endpoint (POST %s): %s\n",
				c.remote.IntrospectionEndpoint, err)
			return false
		}

		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		if c.local.ClientId != "" && c.local.ClientSecret != "" {
			request.SetBasicAuth(c.local.ClientId, c.local.ClientSecret)
		} else {
			fmt.Println("[WARN] Requesting token introspection without " +
				"client credentials (unspecified in the config)")
		}

		response, err := client.Do(request)

		if err != nil {
			fmt.Printf("[ERROR] Failed to call OIDC introspection endpoint "+
				"(POST %s): %s\n", c.remote.IntrospectionEndpoint, err)
			if attemptsCount > 1 {
				fmt.Println("Trying to call OIDC introspection endpoint again after a second...")
				time.Sleep(1 * time.Second)
			} else {
				fmt.Println("[ERROR] Too many failed attempts for JWT " +
					"introspection. Giving up.")
			}
			attemptsCount--
			continue
		}

		defer response.Body.Close()

		if response.StatusCode != http.StatusOK {
			fmt.Printf("[WARN] JWT introspection call gave non-200 HTTP status %d "+
				"(thus JWT not active)\n", response.StatusCode)
			return false
		}

		body, err := io.ReadAll(response.Body)

		if err != nil {
			fmt.Printf("[WARN] Failed to read JWT introspection response "+
				"body with HTTP status %d (thus JWT not active): %s\n",
				response.StatusCode, err)
			return false
		}

		if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			fmt.Printf("[WARN] JWT introspection endpoint returned non-JSON "+
				"[content-type=%s] HTTP 200 response (thus JWT not active): %s\n",
				response.Header.Get("Content-Type"), body)
			return false
		}

		if len(body) == 0 {
			fmt.Println("[WARN] JWT introspection endpoint returned empty " +
				"HTTP 200 response (thus JWT not active)")
			return false
		}

		var result IntrospectionResponse
		if err := json.Unmarshal(body, &result); err != nil {
			fmt.Printf("Cannot unmarshal JSON from the JWT introspection endpoint: %s", err)
		}

		return result.Active
	}

	return false
}

func validateUrl(providedUrl string) *url.URL {
	parsedUrl, err := url.ParseRequestURI(providedUrl)
	if err != nil {
		fmt.Printf("[ERROR] OIDC configuration URL (%s) could not be "+
			"parsed: %s\n", parsedUrl, err)
		os.Exit(1)
	} else if parsedUrl.Scheme == "" || parsedUrl.Host == "" {
		fmt.Printf("[ERROR] OIDC configuration URL (%s) is not absolute.",
			parsedUrl)
		os.Exit(1)
	}
	return parsedUrl
}

func fetchJson(url *url.URL) []byte {
	res, err := http.Get(url.String())

	if err != nil {
		fmt.Printf("[ERROR] OIDC service configuration (%s) could not be "+
			"loaded: %s.\n", url.String(), err)
		os.Exit(1)
	} else if res.StatusCode != http.StatusOK {
		fmt.Printf("[ERROR] OIDC service configuration (%s) could not be "+
			"loaded (HTTP response status: %d).", url.String(), res.StatusCode)
		os.Exit(1)
	} else if res.Body == nil {
		fmt.Printf("[ERROR] OIDC service configuration (%s) could not be "+
			"loaded (empty response).\n", url.String())
		os.Exit(1)
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Printf("[ERROR] Failed to read the body of the OIDC "+
			"configuration (%s) response: %s\n", url.String(), err)
		os.Exit(1)
	}

	return body
}
