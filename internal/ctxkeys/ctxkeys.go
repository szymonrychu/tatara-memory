// Package ctxkeys defines typed context keys shared across internal packages.
// Centralising the keys here avoids import cycles and ensures that packages
// that write a value (httpapi middleware) and packages that read it (lightrag
// client for log correlation) use identical keys.
package ctxkeys

// requestIDKey is the unexported concrete type for context keys in this package.
type requestIDKey struct{}

// RequestID is the context key for the inbound HTTP request ID.
// httpapi.RequestID middleware stores the value; any downstream package
// (e.g. lightrag HTTPClient) reads it for structured-log correlation.
var RequestID = requestIDKey{}
