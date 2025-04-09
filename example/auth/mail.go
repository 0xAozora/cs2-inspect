package auth

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// IMAP server settings
const (
	IMAPMailbox = "inbox"
)

const searchPattern = `Login Code\s+([A-Z\d]{5})\s+`

var r = regexp.MustCompile(searchPattern)

type Message struct {
	Account string
	Resp    chan string
}

type Mailer struct {
	responders map[string]chan string
	respMutex  sync.Mutex

	client       *imapclient.Client
	CapIMAP4rev2 bool

	Chan chan Message

	IMAPServer   string
	IMAPUsername string
	IMAPPassword string

	log *zerolog.Logger
}

func NewMailer(server, username, password string, logger *zerolog.Logger) *Mailer {

	mailer := Mailer{
		responders:   map[string]chan string{},
		IMAPServer:   server,
		IMAPUsername: username,
		IMAPPassword: password,
		log:          logger,
	}
	mailer.connect()

	return &mailer
}

func (m *Mailer) connect() {

	for {
		c, err := imapclient.DialTLS(m.IMAPServer, nil)
		if err != nil {

			m.log.Err(err).Msg("Failed to connect to the IMAP server")

			time.Sleep(5 * time.Second)
			continue
		}

		// Log in to the IMAP server
		loginCmd := c.Login(m.IMAPUsername, m.IMAPPassword)
		if loginCmd == nil {
			m.log.Error().Msg("Failed to perform a login")
			continue
		}
		if err := loginCmd.Wait(); err != nil {
			m.log.Err(err).Msg("Failed to log in")
			time.Sleep(5 * time.Second)
			continue
		}

		// Select the mailbox
		selectCmd := c.Select(IMAPMailbox, nil)
		if selectCmd == nil {
			m.log.Error().Msg("Failed to perform a select")
			continue
		}

		_, err = selectCmd.Wait()
		if err != nil {
			m.log.Err(err).Msg("Failed to select mailbox")
			time.Sleep(5 * time.Second)
			continue
		}

		m.client = c

		// Check capabilities
		caps := c.Caps()
		if caps != nil {
			if _, ok := caps[imap.CapIMAP4rev2]; ok {
				m.CapIMAP4rev2 = true
			}
		}

		break
	}

}

func (m *Mailer) Get(account string, resp chan string) {
	m.respMutex.Lock()
	// Close prev Connection
	if ch, ok := m.responders[account]; ok {
		ch <- ""
	} else if len(m.responders) == 0 {
		go m.Poller()
	}
	m.responders[account] = resp
	m.respMutex.Unlock()
}

func (m *Mailer) Poller() {

	time.Sleep(5 * time.Second)

	var b bool
	for {
		// Check if any account is waiting for a code
		m.respMutex.Lock()
		b = len(m.responders) == 0
		m.respMutex.Unlock()
		if b {
			break
		}

		// Search for new emails received within the last hour
		// Calculate the cutoff time (1 hour ago)
		cutoffTime := time.Now().Add(-1 * time.Hour)
		searchCmd := m.client.Search(&imap.SearchCriteria{Since: cutoffTime, NotFlag: []imap.Flag{imap.FlagSeen}}, &imap.SearchOptions{ReturnAll: true})
		searchData, err := searchCmd.Wait()
		if err != nil {
			m.log.Err(err).Msg("Failed to search for emails")
			m.connect()
			continue
		}

		m.respMutex.Lock()

		numSet := searchData.All
		if numSet != nil {

			fetchCmd := m.client.Fetch(numSet, &imap.FetchOptions{Envelope: true, BodySection: []*imap.FetchItemBodySection{{
				Specifier: imap.PartSpecifierText,
			}}})

			for next := fetchCmd.Next(); next != nil; next = fetchCmd.Next() {
				msgBuffer, err := next.Collect()
				if err != nil {
					m.log.Err(err).
						Uint32("seq", next.SeqNum).
						Msg("Failed to fetch email")
				}

				m.checkMessage(msgBuffer)
			}

			m.log.Debug().Msg("Finished checking emails")
			_ = fetchCmd.Close()
		}
		m.respMutex.Unlock()

		time.Sleep(5 * time.Second)
	}
}

func (m *Mailer) checkMessage(msg *imapclient.FetchMessageBuffer) bool {

	var ok bool
	var ch chan string
	var account string

	// Check the recipient
	for _, address := range msg.Envelope.To {
		account = address.Mailbox
		fmt.Println(account)
		if ch, ok = m.responders[address.Mailbox]; ok {
			break
		}
	}

	if !ok {
		return false
	}

	m.log.Debug().
		Str("subject", msg.Envelope.Subject).
		Str("from", msg.Envelope.From[0].Host).
		Msg("Found a matching email")

	for _, b := range msg.BodySection {

		// Perform a regex search on the email content
		match := r.FindSubmatch(b.Bytes)
		if len(match) > 1 {
			// Found a matching email with a capture group
			loginCode := match[1] // Capture group content

			log.Debug().
				Str("code", string(loginCode)).
				Msg("Found Login Code")

			ch <- string(loginCode)
			delete(m.responders, account)
			return true
		}
	}

	m.log.Debug().
		Msg("No Login Code found in this mail")

	return false
}
