package auth

import (
	inspect "cs2-inspect"
	"errors"

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

func (a *Authenticator) GetCode(codeType protobuf.EAuthSessionGuardType, callback func(string, protobuf.EAuthSessionGuardType) error) error {

	if codeType != protobuf.EAuthSessionGuardType_k_EAuthSessionGuardType_EmailCode {
		return errors.New("code not supported")
	}

	go func() {
		ch := make(chan string, 1)
		a.mailer.Get(a.Name, ch)
		callback(<-ch, codeType)
	}()

	return nil
}
