package bus

import "errors"

// ErrAuthFailed is returned when network authentication fails.
var ErrAuthFailed = errors.New("bus: network auth failed")

// StaticTokenAuth is a simple Authenticator using a shared secret.
type StaticTokenAuth struct {
	Token_ string
}

func (s *StaticTokenAuth) Token() (string, error) {
	return s.Token_, nil
}

func (s *StaticTokenAuth) Verify(token string) error {
	if token != s.Token_ {
		return ErrAuthFailed
	}
	return nil
}
