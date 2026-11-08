// Package tes provides TES wire models and a context-aware REST client.
// It is independent of Funnel's server and backend implementations.
//
// NewClient accepts an optional HTTP client through WithHTTPClient. Configure
// authentication explicitly through Client.User, Client.Password, or Client.Token;
// the package does not read process environment variables.
package tes
