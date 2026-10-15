//go:build !venera_core

package model

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/OpenListTeam/go-cache"
	"github.com/go-webauthn/webauthn/webauthn"
)

var LoginCache = cache.NewMemCache[int]()

func (u *User) WebAuthnID() []byte {
	bs := make([]byte, 8)
	binary.LittleEndian.PutUint64(bs, uint64(u.ID))
	return bs
}

func (u *User) WebAuthnName() string {
	return u.Username
}

func (u *User) WebAuthnDisplayName() string {
	return u.Username
}

func (u *User) WebAuthnCredentials() []webauthn.Credential {
	var res []webauthn.Credential
	err := json.Unmarshal([]byte(u.Authn), &res)
	if err != nil {
		fmt.Println(err)
	}
	return res
}

func (u *User) WebAuthnIcon() string {
	return "https://res.oplist.org/logo/logo.svg"
}
