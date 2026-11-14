package oauth

// EncodeRefreshSecret returns the uma "secret" envelope that stores a refresh
// token at a grant's SecretRef. Persist it with the store's write path; never
// log it.
func EncodeRefreshSecret(refreshToken string) ([]byte, error) {
	return encodeSecret(refreshToken)
}

// NewRefreshGrant returns the validated refresh_token grant produced by an
// interactive consent: tokenURL and clientID of the authorization server
// client, secretRef where the refresh token envelope is stored,
// clientSecretRef where a confidential client's secret is stored (empty for
// public PKCE clients). scopes are
// the granted scopes (nil when the grant should not pin any).
func NewRefreshGrant(tokenURL, clientID, secretRef, clientSecretRef string, scopes []string) (Grant, error) {
	g := Grant{
		Kind: RefreshToken, TokenURL: tokenURL, ClientID: clientID,
		SecretRef: secretRef, ClientSecretRef: clientSecretRef, Scopes: append([]string(nil), scopes...),
	}
	if err := g.Validate(); err != nil {
		return Grant{}, err
	}
	return g, nil
}
