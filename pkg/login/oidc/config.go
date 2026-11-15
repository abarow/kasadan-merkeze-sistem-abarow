// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"fmt"
	"os"
	"strings"
)

// LoadConfig reads the setting from the install's environment variables:
//
//	ABAROW_OIDC_ISSUER             the IdP address (if empty OIDC is not installed)
//	ABAROW_OIDC_CLIENT_ID          client id
//	ABAROW_OIDC_CLIENT_SECRET_FILE the file of the client secret (not given for an
//	                               open client with PKCE)
//	ABAROW_OIDC_REDIRECT_URL       the return address registered with the IdP
//	ABAROW_OIDC_LABEL              button text (optional)
//
// The secret is given in a file, not in an environment variable (bound as a secret in
// the install); so it does not show in the process list or the logs. If Issuer is empty
// the second return value is false.
func LoadConfig(getenv func(string) string) (Config, bool, error) {
	issuer := getenv("ABAROW_OIDC_ISSUER")
	if issuer == "" {
		return Config{}, false, nil
	}
	cfg := Config{
		Issuer: issuer, ClientID: getenv("ABAROW_OIDC_CLIENT_ID"),
		RedirectURL: getenv("ABAROW_OIDC_REDIRECT_URL"), Label: getenv("ABAROW_OIDC_LABEL"),
	}
	if path := getenv("ABAROW_OIDC_CLIENT_SECRET_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, false, fmt.Errorf("oidc: istemci sırrı okunamadı: %w", err)
		}
		cfg.ClientSecret = strings.TrimSpace(string(b))
	}
	if err := cfg.validate(); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}
