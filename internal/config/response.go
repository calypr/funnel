package config

import schema "github.com/calypr/funnel/config"

// ResponseBytes resolves the server's bounded gRPC response limit.
func ResponseBytes(s *schema.Server) int {
	if s == nil || s.MaxResponseBytes == 0 {
		return 4 * 1024 * 1024
	}
	return int(s.MaxResponseBytes)
}
