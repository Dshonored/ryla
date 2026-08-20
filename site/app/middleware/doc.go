// Package middleware holds this application's HTTP middleware.
//
// Framework middleware — request IDs, logging, panic recovery — is applied in
// app.New. Anything specific to this application belongs here.
//
// Create one with:
//
//	ry make:middleware EnsureAdmin
package middleware
