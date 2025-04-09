package auth

import (
	inspect "cs2-inspect"
	"fmt"

	"github.com/0xAozora/go-steam"
	"github.com/0xAozora/go-steam/protocol/protobuf"
)

type AuthenticationHandler struct {
	mailer *Mailer
}

func NewAuthenticationHandler(mailer *Mailer) AuthenticationHandler {
	return AuthenticationHandler{
		mailer: mailer,
	}
}

func (h AuthenticationHandler) NewAuthenticator(bot *inspect.Bot) steam.Authenticator {
	return &Authenticator{
		Name:   bot.Name,
		mailer: h.mailer,
	}
}

type Authenticator struct {
	Name   string
	mailer *Mailer
}

func (a *Authenticator) GetCode(codeType protobuf.EAuthSessionGuardType) string {

	if codeType != protobuf.EAuthSessionGuardType_k_EAuthSessionGuardType_EmailCode {
		fmt.Println("Code Not Supported")
		return ""
	}

	ch := make(chan string, 1)
	a.mailer.Get(a.Name, ch)

	return <-ch
}
