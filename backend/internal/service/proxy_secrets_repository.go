package service

import "fmt"

// DecodeProxyPasswordInPlace decrypts a proxy password for service use. It is
// shared by the repository wrapper and by call sites that eager-load proxies
// outside ProxyRepository (account hydration).
//
// needsRewrite reports legacy plaintext / legacy managed ciphertext so the
// caller can lazily migrate the row to the v2 format.
func DecodeProxyPasswordInPlace(p *Proxy, vault SecretVault) (needsRewrite bool, err error) {
	if p == nil {
		return false, nil
	}
	plaintext, rewrite, err := DecryptProxyPassword(vault, p.Password)
	if err != nil {
		return false, fmt.Errorf("proxy %d password cannot be decrypted: %w", p.ID, err)
	}
	p.Password = plaintext
	return rewrite, nil
}
