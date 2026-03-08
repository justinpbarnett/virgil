// Package server defines extension points for the HTTP server.
//
// Cloud deployments use middleware to layer auth, billing, rate limiting,
// and multi-tenant isolation on top of the core server.
package server

import "net/http"

// Middleware wraps an HTTP handler to add cross-cutting concerns.
// Middleware functions are chained: each wraps the next handler.
//
// Example usage for cloud auth:
//
//	func AuthMiddleware(next http.Handler) http.Handler {
//	    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	        if !validateToken(r) {
//	            http.Error(w, "unauthorized", 401)
//	            return
//	        }
//	        next.ServeHTTP(w, r)
//	    })
//	}
type Middleware func(next http.Handler) http.Handler
