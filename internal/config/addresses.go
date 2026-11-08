package config

import schema "github.com/calypr/funnel/api/config"

// HTTPAddress returns the HTTP address based on HostName and HTTPPort
func HTTPAddress(c *schema.Server) string {
	http := ""
	if c.HostName != "" {
		http = "http://" + c.HostName
	}
	if c.HTTPPort != "" {
		http = http + ":" + c.HTTPPort
	}
	return http
}

// RPCAddress returns the RPC address based on HostName and RPCPort
func RPCAddress(c *schema.Server) string {
	rpc := c.HostName
	if c.RPCPort != "" {
		rpc = rpc + ":" + c.RPCPort
	}
	return rpc
}
