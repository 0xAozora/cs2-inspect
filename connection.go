package inspect

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/0xAozora/go-steam"
	"github.com/0xAozora/go-steam/netutil"
	nb_dialer "github.com/0xAozora/non-blocking-dialer"
)

type DummyConn struct {
	net.Conn
}

type TrojanDialer struct {
	net.Conn
}

func (d *TrojanDialer) Dial(network, address string) (net.Conn, error) {
	return d.Conn, nil
}

type ConnectionStep uint8

const (
	PROXY = iota
	DIRECT

	// SOCKS5 Steps
	GREETING
	AUTH
	CONNECT
)

type NonBlockConn struct {
	nb_dialer.NonBlockConn
	index    int
	conntype ConnectionStep // If direct connection or proxy connection, also encodes information about which step we are in the socks5 process
}

func (h *Handler) pollConnections() {

	var conns []net.Conn
	var err error
	for {

		conns, err = h.writePoller.Wait(10)
		if err != nil {
			h.log.Fatal().Err(err).Msg("EpollW Error")
		}

		for _, dummyConn := range conns {

			h.connMutex.Lock()
			nbconn := h.conns[dummyConn]
			delete(h.conns, dummyConn)
			h.connMutex.Unlock()

			if nbconn == nil {
				continue
			}

			bot := h.botQueue[nbconn.index]
			conn, err := nbconn.Validate()
			if err != nil {

				destination := "Proxy"
				if nbconn.conntype == DIRECT {
					destination = "CM"
				}

				err = fmt.Errorf("failed validating connection to %s: %v", destination, err)
				h.handleError(bot, conn, 5*time.Second, err)
				continue
			}

			// I guess we can set it back to blocking
			nbconn.SetNonBlock(false)

			if nbconn.conntype == DIRECT {
				h.finalizeConnection(bot, conn)
			} else {

				// We cannot yet abandon the nbconn since we aren't finished
				// with establishing the connection to the steam servers
				// and it contains information about the state of the connection
				// So we map our actual conn to it
				h.connMutex.Lock()
				h.conns[conn] = nbconn
				h.connMutex.Unlock()

				nbconn.conntype = GREETING
				err := h.sendSocks5Greeting(bot, conn)
				if err != nil {
					h.handleError(bot, conn, 5*time.Second, err)

					h.connMutex.Lock()
					delete(h.conns, conn)
					h.connMutex.Unlock()
					continue
				}
			}

			h.botMutex.Lock()
			h.bots[conn] = bot
			h.botMutex.Unlock()

			_ = h.readPoller.Add(conn, nbconn.GetFD())
		}
	}
}

func (h *Handler) finalizeConnection(bot *Bot, conn net.Conn) {

	bot.client.Proxy = &TrojanDialer{conn}
	bot.client.ConnectTo(&netutil.PortAddr{})
	bot.status = CONNECTED

	bot.log.Info().
		Str("bot", bot.Name).
		Msg("Connection established")
}

// When we encounter an error, we won't try connecting again (except the Connect function)
func (h *Handler) connectBot(bot *Bot) {

	var destination string
	botConn := NonBlockConn{
		index: bot.index,
	}

	// Usually sticky IP is determined on unique password, while proxy IP and username can be the same
	// So we use the password slice as an identifier if we have enough unique proxies
	// If so we use a proxy address available
	if bot.index > len(h.proxyList.Passwords)-1 {
		h.log.Warn().
			Str("bot", bot.Name).
			Msg("No Proxy available for this Bot")

		if !h.ignoreProxy {
			return
		}

		// Dial to CM directly
		botConn.conntype = DIRECT
		cm := steam.GetRandomCM()
		destination = cm.String()

	} else if h.proxyList.Address != "" {
		destination = h.proxyList.Address
	} else if bot.index < len(h.proxyList.Addresses) {
		destination = h.proxyList.Addresses[bot.index]
	} else {
		h.log.Warn().
			Str("bot", bot.Name).
			Msg("No Proxy address available for this Bot")
		return
	}

	to := "Proxy"
	if botConn.conntype == DIRECT {
		to = "CM"
	}

	dialer := nb_dialer.Dialer{}
	nbconn, err := dialer.Dial("tcp", destination)
	if err != nil {

		h.log.Fatal().
			Err(err).
			Str("bot", bot.Name).
			Str("destination", destination).
			Msgf("Failed dialing to %s", to)
	}

	h.log.Info().
		Str("bot", bot.Name).
		Str("destination", destination).
		Msgf("Dialing to %s", to)

	bot.fd = nbconn.GetFD()
	botConn.NonBlockConn = nbconn

	// Add to write poller
	var dummyConn DummyConn

	h.connMutex.Lock()
	h.conns[&dummyConn] = &botConn
	h.connMutex.Unlock()

	_ = h.writePoller.Add(&dummyConn, bot.fd)
}

func (h *Handler) sendSocks5Greeting(bot *Bot, conn net.Conn) error {

	h.log.Debug().
		Str("bot", bot.Name).
		Msg("Sending SOCKS5 greeting")

	// Send: [0x05][0x01][0x02]
	//        VER  NMETHODS  METHODS
	_, err := conn.Write([]byte{
		0x05, // SOCKS version
		0x01, // Number of authentication methods
		0x02, // Username/password authentication
	})

	return err
}

func (h *Handler) handleSocks5(bot *Bot, conn net.Conn) {

	h.connMutex.Lock()
	nbconn := h.conns[conn]
	h.connMutex.Unlock()

	var err error
	switch nbconn.conntype {

	case GREETING:
		nbconn.conntype = AUTH
		err = h.handleSocksGreeting(bot, conn)
		if err == nil {
			err = h.socks5AuthRequest(bot, conn)
		}
	case AUTH:
		nbconn.conntype = CONNECT
		err = h.handleSocksAuth(bot, conn)
		if err == nil {
			err = h.socks5ConnectRequest(bot, conn)
		}
	case CONNECT:
		err = h.handleSocksConnect(bot, conn)
		if err == nil {
			h.finalizeConnection(bot, conn)
		}
	}

	if err != nil {
		err = fmt.Errorf("failed handling SOCKS5 connection: %v", err)
		h.handleError(bot, conn, 5*time.Second, err)

		// Remove Socks specific info
		h.connMutex.Lock()
		delete(h.conns, conn)
		h.connMutex.Unlock()
	}
}

func (h *Handler) handleSocksGreeting(bot *Bot, conn net.Conn) error {

	h.log.Debug().
		Str("bot", bot.Name).
		Msg("Handle SOCKS5 greeting")

	var reply [2]byte
	_, err := conn.Read(reply[:])

	if err != nil {
		return err
	}

	if reply[0] != 0x05 || reply[1] != 0x02 {
		return fmt.Errorf("server does not accept username/password auth: %v", reply)
	}

	return nil
}

func (h *Handler) socks5AuthRequest(bot *Bot, conn net.Conn) error {

	h.log.Debug().
		Str("bot", bot.Name).
		Msg("Sending SOCKS5 auth request")

	var user string
	if h.proxyList.Username != "" {
		user = h.proxyList.Username
	} else {
		user = h.proxyList.Usernames[bot.index]
	}

	password := h.proxyList.Passwords[bot.index]

	/*
		+----+------+----------+------+----------+
		|VER | ULEN |  UNAME   | PLEN |  PASSWD  |
		+----+------+----------+------+----------+
		| 1  |  1   | 1 to 255 |  1   | 1 to 255 |
		+----+------+----------+------+----------+
	*/

	auth := []byte{0x01}                     // Auth version
	auth = append(auth, byte(len(user)))     // Username length
	auth = append(auth, user...)             // Username
	auth = append(auth, byte(len(password))) // Password length
	auth = append(auth, password...)         // Password

	_, err := conn.Write(auth)
	if err != nil {
		return err
	}

	return nil
}

func (h *Handler) handleSocksAuth(bot *Bot, conn net.Conn) error {

	h.log.Debug().
		Str("bot", bot.Name).
		Msg("Handle SOCKS5 auth response")

	authReply := make([]byte, 2)
	_, err := conn.Read(authReply)

	if err != nil {
		return err
	}

	if authReply[1] != 0x00 {
		return errors.New("username/password authentication failed")
	}

	return nil
}

func (h *Handler) socks5ConnectRequest(bot *Bot, conn net.Conn) error {

	h.log.Debug().
		Str("bot", bot.Name).
		Msg("Sending SOCKS5 connect request")

	cm := steam.GetRandomCM()
	dest := cm.String()

	bot.log.Info().
		Str("bot", bot.Name).
		Str("cm", cm.String()).
		Msg("Connecting to CM")

	addrBytes, _ := parseIPv4Socks5Destination(dest)

	req := []byte{
		0x05, // SOCKS version
		0x01, // CONNECT
		0x00, // Reserved
	}
	req = append(req, addrBytes...)

	_, err := conn.Write(req)
	return err
}

func (h *Handler) handleSocksConnect(bot *Bot, conn net.Conn) error {

	h.log.Debug().
		Str("bot", bot.Name).
		Msg("Handle SOCKS5 connect response")

	resp := make([]byte, 4)
	_, err := conn.Read(resp)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("connection failed, code: %x", resp[1])
	}

	// Read remaining address info
	var addrLen int
	switch resp[3] {
	case 0x01: // IPv4
		addrLen = 4
	case 0x03: // Domain
		lenByte := make([]byte, 1)
		_, _ = io.ReadFull(conn, lenByte)
		addrLen = int(lenByte[0])
	case 0x04: // IPv6
		addrLen = 16
	default:
		return fmt.Errorf("unknown address type: %x", resp[3])
	}

	_, err = io.CopyN(io.Discard, conn, int64(addrLen+2)) // +2 for port
	if err != nil {
		return fmt.Errorf("failed to skip bound address info: %v", err)
	}

	return nil
}

func parseIPv4Socks5Destination(dest string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(dest)
	if err != nil {
		return nil, fmt.Errorf("invalid dest format: %w", err)
	}

	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("not a valid IPv4 address: %s", host)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %s", portStr)
	}

	portBytes := []byte{byte(port >> 8), byte(port & 0xff)}
	ip4 := ip.To4()

	return append([]byte{
		0x01, // ATYP: IPv4
		ip4[0], ip4[1], ip4[2], ip4[3],
	}, portBytes...), nil
}
