package inspect

import (
	"errors"
	"time"

	"github.com/0xAozora/go-steam"
	"github.com/0xAozora/go-steam/protocol/protobuf"
	"github.com/0xAozora/go-steam/totp"
)

type AuthenticationHandler interface {
	NewAuthenticator(bot *Bot) steam.Authenticator
}

type TwoFactorAuthenticator struct {
	bot *Bot
}

func (a *TwoFactorAuthenticator) GetCode(codeType protobuf.EAuthSessionGuardType, callback func(string, protobuf.EAuthSessionGuardType) error) error {

	if codeType != protobuf.EAuthSessionGuardType_k_EAuthSessionGuardType_DeviceCode {
		return errors.New("code not supported")
	}

	code, err := totp.GenerateTotpCode(a.bot.SharedSecret, time.Now())
	if err != nil {
		a.bot.log.Err(err).Msg("Failed to generate TOTP code")
	}

	return callback(code, codeType)
}
