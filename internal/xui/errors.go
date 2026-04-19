package xui

import "errors"

var (
	// ErrUnauthorized indicates the XUI panel returned 401 Unauthorized.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden indicates the XUI panel returned 403 Forbidden.
	ErrForbidden = errors.New("forbidden")
	// ErrBadRequest indicates the XUI panel returned 400 Bad Request.
	ErrBadRequest = errors.New("bad request")
	// ErrNotFound indicates the XUI panel returned 404 Not Found.
	ErrNotFound = errors.New("not found")
	// ErrServerError indicates the XUI panel returned a 5xx error.
	ErrServerError = errors.New("server error")
)
