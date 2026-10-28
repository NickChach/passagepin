package pdf

import "fmt"

// UnsupportedFeatureError identifies an extraction limitation without callers
// having to match human-readable parser messages. Other errors remain intact.
type UnsupportedFeatureError struct {
	Feature string
	Detail  string
}

func (e *UnsupportedFeatureError) Error() string { return e.Detail }

func unsupported(feature, format string, args ...any) error {
	return &UnsupportedFeatureError{Feature: feature, Detail: fmt.Sprintf(format, args...)}
}

// DocumentError means that continuing with earlier page results is unsafe.
// Ordinary page syntax/font errors do not invalidate independently read pages.
type DocumentError struct{ Err error }

func (e *DocumentError) Error() string { return e.Err.Error() }
func (e *DocumentError) Unwrap() error { return e.Err }
