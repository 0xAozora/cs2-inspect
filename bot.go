package inspect

import (
	"net"
	"time"
	"unsafe"

	cs2 "github.com/0xAozora/cs2-inspect/cs2/protocol/protobuf"

	"github.com/0xAozora/epoller"
	"github.com/0xAozora/go-steam/protocol/gamecoordinator"
	"github.com/rs/zerolog"
	"golang.org/x/net/proxy"

	"github.com/0xAozora/go-steam"
)

type BotStatus uint8

const (
	DISCONNECTED BotStatus = iota
	CONNECTED
	LOGGED_IN
	INGAME
)

type Credentials struct {
	Name         string
	Password     string
	SharedSecret string
}

type Bot struct {
	client      *steam.Client
	fd          uint64 // File descriptor for this bots connection
	lastInspect time.Time

	Credentials

	index int // Index in the Bot Queue

	status BotStatus
	//steamStatus *uint8 // TODO: Implement Global Steam status and point to it to correctly handle instances like offline Steam Servers

	log *zerolog.Logger
}

func NewBot(Credentials Credentials, logger *zerolog.Logger) *Bot {
	client := steam.NewManualClient()
	client.Auth = &steam.Auth{Client: client}
	client.GC = &steam.GameCoordinator{Client: client}

	return &Bot{
		Credentials: Credentials,
		client:      client,
		log:         logger,
	}
}

func (bot *Bot) Connect(dialer proxy.Dialer) net.Conn {

	bot.client.Proxy = dialer
	for {
		cm := steam.GetRandomCM()

		bot.log.Info().
			Str("bot", bot.Name).
			Str("cm", cm.String()).
			Msg("Connecting to CM")

		err := bot.client.ConnectTo(cm)
		if err == nil {
			break
		}

		bot.log.Err(err).
			Str("bot", bot.Name).
			Msg("Failed to connect to CM")

		time.Sleep(5 * time.Second)
	}

	bot.log.Info().
		Str("bot", bot.Name).
		Msg("Connection established")

	bot.status = CONNECTED

	conn := getTCPConn(bot.client)
	bot.fd = epoller.GetFD(unsafe.Pointer(conn))

	return conn
}

func (bot *Bot) Login(refreshToken string, auth steam.Authenticator) {

	if refreshToken == "" {
		bot.client.Auth.Authenticator = auth
		bot.client.Auth.LogOnCredentials(&steam.LogOnDetails{
			Username:               bot.Name,
			Password:               bot.Password,
			ShouldRememberPassword: true,
		})
	} else {
		bot.client.Auth.LogOn(&steam.LogOnDetails{
			Username:               bot.Name,
			RefreshToken:           refreshToken,
			ShouldRememberPassword: true,
		})
	}
}

func (bot *Bot) Inspect(s, a, d, m uint64) error {

	var sptr, mptr *uint64
	if s != 0 {
		sptr = &s
	}
	if m != 0 {
		mptr = &m
	}

	bot.client.GC.Write(gamecoordinator.NewGCMsgProtobuf(
		730,
		uint32(cs2.ECsgoGCMsg_k_EMsgGCCStrike15_v2_Client2GCEconPreviewDataBlockRequest),
		&cs2.CMsgGCCStrike15V2_Client2GCEconPreviewDataBlockRequest{
			ParamS: sptr,
			ParamA: &a,
			ParamD: &d,
			ParamM: mptr,
		}, //"76561198133242371", "28407485710", "1017934894481355924"
	))

	return nil
}
