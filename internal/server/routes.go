package server

import (
	"encoding/json"
	"net/http"

	"github.com/calypr/funnel/internal/events"
	serverauth "github.com/calypr/funnel/internal/server/auth"
	"github.com/calypr/funnel/internal/server/gateway"
	"github.com/calypr/funnel/internal/server/middleware"

	"github.com/calypr/funnel/internal/webdash"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func (s *Server) routes(auth *serverauth.Authentication, grpcMux http.Handler) http.Handler {
	mux := http.NewServeMux()
	// Web dashboard
	dashmux := http.NewServeMux()
	dashmux.Handle("/", webdash.RootHandler())
	dashfs := webdash.FileServer()
	mux.Handle("/favicon.ico", dashfs)
	mux.Handle("/manifest.json", dashfs)

	// Health and metrics
	mux.Handle("/health.html", dashfs)
	mux.HandleFunc("/healthz", healthHandler)
	mux.Handle("/static/", dashfs)
	mux.Handle("/metrics", promhttp.Handler())

	// Login
	mux.HandleFunc("/login", auth.LoginHandler)
	mux.HandleFunc("/login/token", auth.EchoTokenHandler)

	// Root
	mux.HandleFunc("/", func(resp http.ResponseWriter, req *http.Request) {

		// Pass header to plugin if plugin is enabled
		if s.Plugins != nil {
			req = middleware.ForwardPluginHeaders(req)
		}
		// TODO this doesnt handle all routes
		if s.OidcAuth != nil && s.OidcAuth.ServiceConfigURL == "" && len(s.BasicAuth) > 0 {
			resp.Header().Set("WWW-Authenticate", "Basic")
		}
		switch gateway.ResponseType(req) {
		case "html":
			// HTML was requested (by the browser)
			dashmux.ServeHTTP(resp, req)
		default:
			// Set "cache-control: no-store" to disable response caching.
			// Without this, some servers (e.g. GCE) will cache a response from ListTasks, GetTask, etc.
			// which results in confusion about the stale data.
			if s.DisableHTTPCache {
				resp.Header().Set("Cache-Control", "no-store")
			}

			// Apply message middleware to grpcMux
			middleware.MessageHandler(gateway.WithTaskView(grpcMux)).ServeHTTP(resp, req)
		}
	})

	return mux
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	// Include logging metrics in health check
	stdoutDropped, stderrDropped, total := events.GetLogEventStats()

	health := map[string]interface{}{
		"status": "OK",
		"log_metrics": map[string]int64{
			"stdout_events_dropped": stdoutDropped,
			"stderr_events_dropped": stderrDropped,
			"total_events":          total,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(health) //nolint:errcheck // Health fields are JSON-compatible; a disconnected client cannot receive another response.
}

// handleError handles errors in the HTTP stack, logging errors, stack traces,
// and returning an HTTP error code.
func (s *Server) handleError(w http.ResponseWriter, req *http.Request, err string, code int) {
	s.Log.Error("HTTP handler error", "method", req.Method, "status", code)
	http.Error(w, err, code)
}
