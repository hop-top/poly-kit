package sync

// TransportOption configures transport construction.
type TransportOption func(*transportOpts)

type transportOpts struct {
	authToken string
}

// WithAuthToken sets the bearer token for authenticated transports.
func WithAuthToken(token string) TransportOption {
	return func(o *transportOpts) { o.authToken = token }
}
