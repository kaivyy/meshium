package auth

type SetupRequest struct {
	Password string `json:"password"`
}

type UnlockRequest struct {
	Password string `json:"password"`
}

type AuthStatus struct {
	Setup  bool `json:"setup"`
	Locked bool `json:"locked"`
}

type SSHKeyResponse struct {
	PublicKey string `json:"publicKey"`
}
