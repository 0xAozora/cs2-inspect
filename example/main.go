package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
	"unsafe"

	inspect "github.com/0xAozora/cs2-inspect"
	"github.com/0xAozora/cs2-inspect/types"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
)

func main() {

	// Logger
	logger := zerolog.New(zerolog.NewConsoleWriter())

	// Load env
	err := godotenv.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("Error loading .env file")
	}

	// TokenDB to store tokens for relogin
	var tokenDB inspect.TokenDB
	//tokenDB, _ = tokendb.NewTokenDB("tokens.db")

	// Metrics Logger to log inspect requests
	var metricsLogger inspect.MetricsLogger
	//metricsLogger = metrics.NewInfluxDB(os.Getenv("INFLUXDB_HOST"),os.Getenv("INFLUXDB_KEY"), os.Getenv("INFLUXDB_ORG"))

	// Auth Handler, if you want to use an Email Authenticator, or a custom Authenticator
	// You could do something fancy like implement an Hashicorp Vault Authenticator
	// Otherwise a default authenticator will be used if a shared secret it provided
	var authHandler inspect.AuthenticationHandler
	//mailer := auth.NewMailer("smtp.example.com", "username", "password", &logger)
	//authHandler = auth.NewAuthenticationHandler(mailer)

	// Handler
	handler, err := inspect.NewHandler(1, 5, 5, nil, true, authHandler, tokenDB, &logger, metricsLogger)
	if err != nil {
		log.Fatal(err)
	}

	bot := inspect.NewBot(inspect.Credentials{
		Name:         os.Getenv("BOT_NAME"),
		Password:     os.Getenv("BOT_PASSWORD"),
		SharedSecret: os.Getenv("BOT_SHARED_SECRET"),
	}, &logger)

	handler.AddBot(bot)

	http.HandleFunc("/status", status(handler))
	http.HandleFunc("/inspect", inspectItem(handler, &logger))
	http.ListenAndServe("localhost:9993", nil)
}

func inspectItem(h *inspect.Handler, logger *zerolog.Logger) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		// If you are wondering why we are using unsafe.Pointer:
		// We could be parsing the user payload directly into the Info Struct,
		// however the user could add extra fields to the struct which we would need to verify in the worst case
		// That leaves us with two options:
		// 1. Parse the payload into the Lookup struct, and then copy the data into the Info struct.
		//    This does work with a few items, but produces more garbage especially for alot of items.
		// 2. Parse the payload into the Lookup struct, and then use unsafe.Pointer to "convert" it to the Info struct
		//    This is a bit more risky, but reuses the memory and you can typecast the whole slice for a bulk lookup
		// We could also have embedded the Lookup struct into the Info struct, however it does not work well with the messagepack code generation
		// to use it in internal messaging systems
		request := types.Request{L: []*types.Info{{}}}
		lookup := (*types.Lookup)(unsafe.Pointer(request.L[0]))

		if err := json.NewDecoder(r.Body).Decode(lookup); err != nil || (lookup.S != 0) == (lookup.M != 0) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		logger.Debug().
			Msg("HTTP Inspect Request")

		offset, c := h.Inspect(&request, []*types.Info{request.L[0]})

		// No capacity
		if offset != 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		select {
		case <-c:
			break
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}

		json.NewEncoder(w).Encode(&request.L[0])
	}
}

func status(h *inspect.Handler) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		status := h.GetBotStatus()

		response := map[string]int{
			"Bots":         status[4],
			"DISCONNECTED": status[0],
			"CONNECTED":    status[1],
			"LOGGED_IN":    status[2],
			"INGAME":       status[3],
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}
