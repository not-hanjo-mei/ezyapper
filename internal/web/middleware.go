package web

import "net/http"

// Middleware wraps an http.Handler with pre/post-processing logic.
type Middleware func(http.Handler) http.Handler

// Chain composes middlewares around a final handler.
// The first middleware in the list becomes the outermost layer.
func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
