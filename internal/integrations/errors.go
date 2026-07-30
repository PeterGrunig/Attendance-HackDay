package integrations

import "errors"

var (
	ErrCapabilityUnsupported = errors.New("integration capability is not supported")
	ErrInvalidConfiguration  = errors.New("integration configuration is invalid")
	ErrAuthentication        = errors.New("integration authentication failed")
	ErrPermission            = errors.New("integration permission denied")
	ErrRateLimited           = errors.New("integration rate limited")
	ErrTemporaryFailure      = errors.New("temporary integration failure")
	ErrPermanentRejection    = errors.New("integration request permanently rejected")
	ErrProviderAlreadyExists = errors.New("integration provider is already registered")
	ErrProviderNotFound      = errors.New("integration provider was not found")
	ErrEncryptionUnavailable = errors.New("integration credential encryption is unavailable")
)
