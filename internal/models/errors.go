package models

import "errors"

var (
	// ErrRecordNotFound indicates a requested record does not exist.
	ErrRecordNotFound = errors.New("record not found")
	// ErrEditConflict indicates the record was modified concurrently.
	ErrEditConflict = errors.New("edit conflict")
	// ErrDuplicateEmail indicates an email address already exists.
	ErrDuplicateEmail = errors.New("duplicate email")
	// ErrDuplicateHandle indicates a handle already exists.
	ErrDuplicateHandle = errors.New("duplicate handle")
	// ErrInvalidCredentials indicates an authentication credential is invalid.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrAuthenticationToken indicates an authentication token is invalid.
	ErrAuthenticationToken = errors.New("invalid authentication token")
)
