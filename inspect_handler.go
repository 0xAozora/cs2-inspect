package inspect

import (
	cs2 "cs2-inspect/cs2/protocol/protobuf"
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xAozora/epoller"
	"github.com/0xAozora/go-steam"
	"github.com/0xAozora/go-steam/protocol"
	"github.com/0xAozora/go-steam/protocol/gamecoordinator"
	"github.com/0xAozora/go-steam/protocol/protobuf"
	"github.com/0xAozora/go-steam/protocol/steamlang"
	"github.com/rs/zerolog"
	"golang.org/x/net/proxy"
	"google.golang.org/protobuf/proto"
)

type Handler struct {
	bots     map[net.Conn]*Bot
	botQueue []*Bot
	botMutex sync.RWMutex // Mutex for everything above

	items        map[uint64]*InspectTask // Map assetID directly to InspectTask
	ItemMutex    sync.Mutex              // Mutex for ItemMap
	InspectMutex sync.Mutex              // Mutex for InspectMap
	Pool         *Pool

	c   chan *InspectTask // Channel for Inspect Requests
	len uint32            // Inspects in flight
	cap uint32            // Capacity of Inspects

	// TimeTree
	timeTree *TimeTree

	//Heartbeat
	heartbeatInterval int32
	heartbeats        map[string]int64 // Map BotName to Next Heartbeat
	heartbeatMutex    sync.Mutex       // Mutex for the map

	epoll epoller.Poller

	metricsLogger MetricsLogger
	tokenDB       TokenDB

	log *zerolog.Logger

	// Custom AuthenticationHandler
	authenticationHandler AuthenticationHandler

	// Proxy
	proxyList   *ProxyList
	ignoreProxy bool
}

// NewHandler creates a new Handler instance
func NewHandler(len, cap, poolsize int, proxyList *ProxyList, ignoreProxy bool, auth AuthenticationHandler, tokenDB TokenDB, logger *zerolog.Logger, metricsLogger MetricsLogger) (*Handler, error) {

	if tokenDB == nil {
		tokenDB = &StubDB{}
	}
	if metricsLogger == nil {
		metricsLogger = &StubMetrics{}
	}
	if logger == nil {
		l := zerolog.New(zerolog.NewConsoleWriter())
		logger = &l
	}
	if proxyList == nil {
		if !ignoreProxy {
			return nil, errors.New("proxy list is nil")
		}
		proxyList = &ProxyList{}
	}

	epoll, err := epoller.NewPoller()
	if err != nil {
		return nil, err
	}

	timeTree := NewTimeTree()

	handler := Handler{
		bots:     make(map[net.Conn]*Bot),
		botQueue: make([]*Bot, 0, len),
		items:    make(map[uint64]*InspectTask),
		epoll:    epoll,
		tokenDB:  tokenDB,

		timeTree:          timeTree,
		heartbeatInterval: 9, // Just in case steam returns 0
		heartbeats:        make(map[string]int64),

		c:   make(chan *InspectTask, cap-len),
		cap: uint32(cap),

		authenticationHandler: auth,
		metricsLogger:         metricsLogger,
		log:                   logger,

		Pool: NewPool(poolsize, poolsize, 10),

		proxyList:   proxyList,
		ignoreProxy: ignoreProxy,
	}

	go timeTree.Run(handler.handleTask)

	initializeSteamDirectory(logger)
	go func() {
		for {
			time.Sleep(time.Hour)
			initializeSteamDirectory(logger)
		}
	}()

	go handler.handleClients()

	go handler.inspectLoop()

	return &handler, nil
}

func (h *Handler) AddBot(bot *Bot) error {

	// Safety check
	if bot == nil {
		return errors.New("bot is nil")
	}

	// Make sure bot has a logger, else use default logger of the handler
	if bot.log == nil {
		bot.log = h.log
	}

	h.botMutex.Lock()
	h.botQueue = append(h.botQueue, bot)
	bot.index = len(h.botQueue) - 1
	h.botMutex.Unlock()

	// Connect
	h.Pool.Schedule(func() {
		h.connectBot(bot)
	})

	return nil
}

// When we encounter an error, we won't try connecting again (except the Connect function)
func (h *Handler) connectBot(bot *Bot) {

	// Usually sticky IP is determined on unique password, while proxy IP and username can be the same
	// So we use the password slice as an identifier if we have enough unique proxies
	if bot.index > len(h.proxyList.Passwords)-1 {
		h.log.Warn().
			Str("bot", bot.Name).
			Msg("No Proxy available for this Bot")

		if !h.ignoreProxy {
			return
		}

		conn := bot.Connect(nil)
		_ = h.epoll.Add(conn, bot.fd)

		h.botMutex.Lock()
		h.bots[conn] = bot
		h.botMutex.Unlock()
		return
	}

	var addr string
	if h.proxyList.Address != "" {
		addr = h.proxyList.Address
	} else {
		addr = h.proxyList.Addresses[bot.index]
	}

	var user string
	if h.proxyList.Username != "" {
		user = h.proxyList.Username
	} else {
		user = h.proxyList.Usernames[bot.index]
	}

	dialer, err := proxy.SOCKS5("tcp", addr, &proxy.Auth{User: user, Password: h.proxyList.Passwords[bot.index]}, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		h.log.Err(err).
			Str("bot", bot.Name).
			Str("proxy", addr).
			Msg("Error creating proxy dialer")
		return
	}

	conn := bot.Connect(dialer)
	_ = h.epoll.Add(conn, bot.fd)

	h.botMutex.Lock()
	h.bots[conn] = bot
	h.botMutex.Unlock()
}

func (h *Handler) loginBot(bot *Bot) {

	token, _ := h.tokenDB.GetToken(bot.Name)
	if token != "" {
		bot.Login(token, nil)
		return
	}

	var steamAuthenticator steam.Authenticator
	if bot.SharedSecret != "" {
		steamAuthenticator = &TwoFactorAuthenticator{bot: bot}
	} else if h.authenticationHandler != nil {
		steamAuthenticator = h.authenticationHandler.NewAuthenticator(bot)
	} else {
		h.log.Error().
			Str("bot", bot.Name).
			Msg("No Authenticator available")
	}

	bot.Login("", steamAuthenticator)
}

// Handle Heartbeat, Inspect Timeouts and Tasks
func (h *Handler) handleTask(task *Task) {

	switch task.T {

	// Heartbeat
	case Heartbeat:

		now := time.Now().UnixNano()

		index := task.Value.(int)
		bot := h.botQueue[index]

		h.log.Debug().
			Str("bot", bot.Name).
			Msg("Heartbeat")

		h.heartbeatMutex.Lock()
		next, ok := h.heartbeats[bot.Name]
		if !ok || next > now {
			h.heartbeatMutex.Unlock()

			h.log.Debug().
				Str("bot", bot.Name).
				Bool("ok", ok).
				Int64("next", next).
				Int64("now", now).
				Msg("Stopping Heartbeat")

			return
		}

		h.Pool.Schedule(func() {
			err := bot.client.Write(protocol.NewClientMsgProtobuf(steamlang.EMsg_ClientHeartBeat, new(protobuf.CMsgClientHeartBeat)))
			if err != nil {
				return
			}
		})

		//time.Sleep(1 * time.Second) // When making changes, make sure to sleep when testing to not spam the servers in case something goes wrong

		// Add back the same task with next heartbeat time
		task.Time += int64(h.heartbeatInterval) * int64(time.Second)
		h.timeTree.AddTask(task)

		h.heartbeats[bot.Name] = task.Time

		h.heartbeatMutex.Unlock()

	// Check inspect timeout
	case InspectTimeout:
		assetid := task.Value.(uint64)
		h.ItemMutex.Lock()
		inspectTask := h.items[assetid]
		// TODO: Implement InspectTask Context and retry if doable in time
		if inspectTask != nil {
			new := atomic.AddUint32(&inspectTask.Remaining, ^uint32(0))
			if new == 0 {
				inspectTask.Ret <- struct{}{}
			}
			delete(h.items, assetid)
		}
		h.ItemMutex.Unlock()

	// Scheduled function
	case Function:
		h.Pool.Schedule(task.Value.(func()))
	}
}

func (h *Handler) handleClients() {

	var conns []net.Conn
	var err error
	for {

		conns, err = h.epoll.Wait(100)
		if err != nil {
			h.log.Fatal().Err(err).Msg("Epoll Error")
		}

		for _, conn := range conns {

			h.botMutex.RLock()
			bot := h.bots[conn]
			h.botMutex.RUnlock()

			// Avoid Read of Broken Connection
			if bot == nil {
				continue
			}

			packet, err := bot.client.Read()
			if err != nil {
				h.handleError(bot, conn, 0, err)
				continue
			}

			h.Pool.Schedule(func() {
				h.handlePacket(bot, conn, packet)
			})
		}
	}
}

func (h *Handler) handleError(bot *Bot, conn net.Conn, sleep time.Duration, err error) {

	// StackTrace, we need to know where the error happened
	buf := make([]byte, 1024) // Initial buffer size
	for {
		n := runtime.Stack(buf, false)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		// Increase buffer size and try again if it was too small
		buf = make([]byte, 2*len(buf))
	}

	// Log Error with Stacktrace so we know where it came from
	h.log.Err(err).
		Str("bot", bot.Name).
		Str("stack", string(buf)).
		Msg("Stacktrace")

	// Remove Heartbeat Task
	h.removeHeartbeat(bot)

	// Remove File Descriptor from poller to avoid EOF error
	if bot.status != DISCONNECTED {

		h.botMutex.Lock()
		delete(h.bots, conn)
		_ = h.epoll.Remove(bot.fd)
		h.botMutex.Unlock()

		bot.client.Disconnect()
	}

	h.log.Info().
		Str("bot", bot.Name).
		Msg("Disconnected")

	bot.status = DISCONNECTED

	if sleep == 0 {
		sleep = 5 * time.Second
	}

	// Try reconnecting
	h.timeTree.AddTask(&Task{
		T: Function,
		Value: func() {
			h.connectBot(bot)
		},
		Time: time.Now().Add(sleep).UnixNano(),
	})
}

// TODO: Make specific methods for each packet type
func (h *Handler) handlePacket(bot *Bot, conn net.Conn, packet *protocol.Packet) {

	c := bot.client

	var err error
	switch packet.EMsg {

	// General
	case steamlang.EMsg_ChannelEncryptRequest:
		h.log.Info().
			Str("bot", bot.Name).
			Msg("ChannelEncryptRequest")

		err = c.HandleChannelEncryptRequest(packet)
	case steamlang.EMsg_ChannelEncryptResult:
		if err = c.HandleChannelEncryptResult(packet); err == nil {
			// Connected
			h.log.Info().
				Str("bot", bot.Name).
				Msg("Encryption Complete")

			h.loginBot(bot)
		}

	// Multiple Packets
	case steamlang.EMsg_Multi:
		var packets []*protocol.Packet
		packets, err = c.HandleMulti(packet)
		for _, p := range packets {
			h.handlePacket(bot, conn, p)
		}

	// Auth
	case steamlang.EMsg_ClientLogOnResponse: // TODO: Handle EResult
		var l *steam.LoggedOnEvent
		l, err = c.Auth.HandleLogOnResponse(packet)
		if err != nil {
			break
		}

		if l.Result != steamlang.EResult_OK {

			switch l.Result {
			case steamlang.EResult_Expired:
				h.tokenDB.SetToken(bot.Name, "")
				fallthrough
			case steamlang.EResult_TryAnotherCM:
				h.handleError(bot, conn, 0, errors.New("login failed | "+steamlang.EResult_name[l.Result]))
			default:
				h.handleError(bot, conn, 60*time.Second, errors.New("login failed | "+steamlang.EResult_name[l.Result]))
			}
			break
		}

		// Add Heartbeat Task for this Bot
		h.heartbeatInterval = l.InGameSecsPerHeartbeat
		h.heartbeatMutex.Lock()
		next := time.Now().Add(time.Duration(h.heartbeatInterval) * time.Second).UnixNano()
		h.heartbeats[bot.Name] = next
		h.timeTree.AddTask(&Task{
			T:     Heartbeat,
			Value: bot.index,
			Time:  next,
		})
		h.heartbeatMutex.Unlock()

		// Logged In
		bot.status = LOGGED_IN
		h.log.Info().
			Str("bot", bot.Name).
			Msg("Logged In")

		// Save Token
		if bot.client.Auth.Details != nil {
			h.log.Debug().
				Str("bot", bot.Name).
				Str("refresh_token", bot.client.Auth.Details.RefreshToken).
				Msg("Got Refresh Token")

			h.tokenDB.SetToken(bot.Name, bot.client.Auth.Details.RefreshToken)

			// Wipe Memory
			bot.client.Auth.Details = nil
			bot.client.Auth.Authenticator = nil
		}

		// Request Free License
		h.log.Info().
			Str("bot", bot.Name).
			Msg("Requesting free license")

		err = bot.client.Write(protocol.NewClientMsgProtobuf(steamlang.EMsg_ClientRequestFreeLicense, &protobuf.CMsgClientRequestFreeLicense{Appids: []uint32{730}}))

	case steamlang.EMsg_ClientSessionToken:
	case steamlang.EMsg_ClientLoggedOff:
		msg := c.Auth.HandleLoggedOff(packet)

		h.log.Info().
			Str("bot", bot.Name).
			Str("result", steamlang.EResult_name[msg.Result]).
			Msg("Logged off")

		if msg.MinReconnect == 0 {
			msg.MinReconnect = 4
		}

		// Remove Heartbeat Task
		h.removeHeartbeat(bot)

		// Try login after MinReconnect
		h.timeTree.AddTask(&Task{
			T: Function,
			Value: func() {
				h.loginBot(bot)
			},
			Time: time.Now().Add(time.Duration(msg.MinReconnect+1) * time.Second).UnixNano(),
		})

	case steamlang.EMsg_ClientUpdateMachineAuth:
		c.Auth.HandleUpdateMachineAuth(packet)
	case steamlang.EMsg_ClientAccountInfo:
		c.Auth.HandleAccountInfo(packet)
	case steamlang.EMsg_ServiceMethodResponse:
		c.JobMutex.Lock()
		fn := c.JobHandlers[uint64(packet.TargetJobId)]
		delete(c.JobHandlers, uint64(packet.TargetJobId))
		c.JobMutex.Unlock()
		err = fn(packet)
	// Csgo Flow
	case steamlang.EMsg_ClientRequestFreeLicenseResponse:

		h.log.Info().
			Str("bot", bot.Name).
			Msg("Play cs2")

		err = bot.client.GC.SetGamesPlayed(730)
	case steamlang.EMsg_ClientGameConnectTokens:

		h.log.Info().
			Str("bot", bot.Name).
			Msg("Send Hello")

		err = bot.client.GC.Write(gamecoordinator.NewGCMsgProtobuf(730,
			uint32(cs2.EGCBaseClientMsg_k_EMsgGCClientHello),
			&cs2.CMsgClientHello{
				Version: proto.Uint32(2000244),
			},
		))

	// GC
	case steamlang.EMsg_ClientFromGC:

		msg := new(protobuf.CMsgGCClient)
		packet.ReadProtoMsg(msg)

		p, err := gamecoordinator.NewGCPacket(msg)
		if err != nil {
			h.log.Warn().
				Err(err).
				Str("bot", bot.Name).
				Msg("Error reading GC message")

			return
		}

		h.handleGCPacket(bot, p)

	// Other
	default:
		//log.Printf("Bot#%d Unhandled Packet: %s\n", bot.Index, steamlang.EMsg_name[packet.EMsg])
	}

	if err != nil {
		h.handleError(bot, conn, 0, err)
		return
	}
}

func (h *Handler) handleGCPacket(bot *Bot, packet *gamecoordinator.GCPacket) {

	if packet.AppId == 730 {
		switch packet.MsgType {
		case uint32(cs2.EGCBaseClientMsg_k_EMsgGCClientWelcome):

			h.log.Info().
				Str("bot", bot.Name).
				Msg("ClientWelcome")

			bot.status = INGAME

		case uint32(cs2.ECsgoGCMsg_k_EMsgGCCStrike15_v2_Client2GCEconPreviewDataBlockResponse):
			h.handleInspectResponse(bot, packet)
		default:

			h.log.Debug().
				Str("bot", bot.Name).
				Int("msg_type", int(packet.MsgType)).
				Msg("Unhandled GC Message")
		}
	}
}

func (h *Handler) removeHeartbeat(bot *Bot) {
	var ok bool
	for range 3 {
		h.heartbeatMutex.Lock()
		nextHearteat := h.heartbeats[bot.Name]
		if nextHearteat == 0 {
			h.heartbeatMutex.Unlock()
			ok = true
			break
		}
		h.timeTree.RemoveTask(nextHearteat)
		delete(h.heartbeats, bot.Name)
		h.heartbeatMutex.Unlock()
	}
	if !ok {
		h.log.Warn().
			Str("bot", bot.Name).
			Msg("Failed to remove Heartbeat Task 2nd try")
	}
}

func (h *Handler) GetBotStatus() (status [5]int) {
	h.botMutex.RLock()
	status[4] = len(h.botQueue)
	for _, bot := range h.botQueue {
		if bot != nil {
			status[bot.status]++
		}
	}
	h.botMutex.RUnlock()

	return
}

func initializeSteamDirectory(log *zerolog.Logger) {
	err := steam.InitializeSteamDirectory()
	if err != nil {
		log.Err(err).Msg("Error initializing Steam directory")
	}
}
