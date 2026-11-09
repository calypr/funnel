package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"google.golang.org/grpc/metadata"
)

// ForwardPluginHeaders copies request headers into outgoing gRPC metadata.
func ForwardPluginHeaders(req *http.Request) *http.Request {
	md := metadata.New(nil)
	ctxWithMetadata := req.Context() // Start with the request's context
	for key, values := range req.Header {
		for _, value := range values {
			md.Append(key, value)
		}
		ctxWithMetadata = context.WithValue(ctxWithMetadata, key, values)

	}
	ctxWithMetadata = metadata.NewOutgoingContext(ctxWithMetadata, md) // Create context with metadata
	return req.WithContext(ctxWithMetadata)
}

// MessageHandler normalizes gRPC gateway responses that contain Funnel messages.
func MessageHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Match gRPC's default 4 MiB message budget before JSON normalization buffers the body.
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		// Create a response recorder to capture the response
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)

		// Check for the message header in ANY response
		if msg := rec.Header().Get("Grpc-Metadata-X-Funnel-Message"); msg != "" {
			// Write the message as JSON
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", rec.Header().Get("Cache-Control"))
			for k, v := range rec.Header() {
				// Copy headers except the gRPC metadata one
				if !strings.HasPrefix(k, "Grpc-Metadata-") {
					w.Header()[k] = v
				}
			}
			w.WriteHeader(rec.Code)
			response := map[string]string{"message": msg}
			_ = json.NewEncoder(w).Encode(response) //nolint:errcheck // Decoded JSON is serializable; a disconnected client cannot receive another response.
			return                                  // Important: return here to prevent writing the original response
		}

		// No message - copy the original response as-is
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes()) //nolint:errcheck // The proxied response is already committed; a client write failure cannot be recovered here.
	})
}
