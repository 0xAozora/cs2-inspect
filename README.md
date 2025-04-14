
# CS2-Inspect

A Go-based tool for inspecting and retrieving information about CS2 (Counter-Strike 2) items.

## Overview

**CS2-Inspect** is a lightweight, high-performance library designed to retrieve detailed information about Counter-Strike 2 items—including float values, paint seeds, stickers, and keychains. It interacts directly with the CS2 game coordinator to provide accurate and up-to-date item data, all while maintaining minimal overhead.

## Motivation

The decision to build CS2-Inspect stemmed from the high memory usage observed in [csfloat/inspect](https://github.com/csfloat/inspect), particularly when scaling to thousands of bots. To address this, we developed a custom inspect service in Go.

Inspired by projects like [1m-go-websockets](https://github.com/eranyanay/1m-go-websockets) and [gnet](https://github.com/panjf2000/gnet), we explored the use of `epoll` in a client-side context. This approach has the potential to deliver substantial performance gains and memory efficiency.

One current limitation involves the blocking nature of `net.Dialer`, which can delay the initialization process as we wait for all bot connections to be established. This becomes especially problematic when dealing with unreliable proxies or intermittent Steam server availability, potentially stalling the goroutine pool at startup.

## Features

- Inspect CS2 items using various identifiers
- Inspect Inventory Items
- Inspect Market Items
- Bulk Inspect
- Retrieve detailed item information including wear values, stickers, and patterns
- Metrics logging for monitoring
- Token-based authentication system
- Support for custom authenticators
- SOCKS5 Proxy support

## Installation

```bash
go get github.com/0xAozora/cs2-inspect
```

## Usage

Here's a basic example of how to use the library:

```go
package main

import (
    inspect "github.com/0xAozora/cs2-inspect"
    "github.com/rs/zerolog"
    "github.com/joho/godotenv"
    "os"
)

func main() {
    // Initialize logger
    logger := zerolog.New(zerolog.NewConsoleWriter())
    
    // Load environment variables
    err := godotenv.Load()
    if err != nil {
        logger.Fatal().Err(err).Msg("Error loading .env file")
    }
    
    // Create inspect-handler
    // With a bot queue of 1, item queue of 5 and goroutine pool of 5
    // Ignore no proxy
    handler, err := inspect.NewHandler(1, 5, 5, nil, true, nil, nil, logger, nil)

    if err != nil {
        logger.Fatal().Err(err).Msg("Failed to create inspect-handler")
    }
    
    // Add a bot
    bot := inspect.NewBot(inspect.Credentials{
		Name:         os.Getenv("BOT_NAME"),
		Password:     os.Getenv("BOT_PASSWORD"),
		SharedSecret: os.Getenv("BOT_SHARED_SECRET"),
	}, &logger)

	handler.AddBot(bot)

    // Wait for the bot to be ingame 
    for {
        time.Sleep(1 * time.Second)
        if handler.GetBotStatus()[3] == 1 {

            time.Sleep(1 * time.Second)

            // Create a request of two items
            // Asset IDs need to be sorted descending
            request := types.Request{L: []*types.Info{
                {
                    M: 650314264286612614,
                    A: 43120462815,
                    D: 17169673277089413765,
                },
                {
                    S: 76561198133242371,
                    A: 42809792578,
                    D: 7530691771313978885,
                },
            }}

            // Return Buffer
            // Each pointer will point back to our provided info struct if the inspect was successful, nil otherwise
            inspectedInfos := make([]*types.Info, len(request.L))

            // Inspect
            ok, c := handler.Inspect(&request, inspectedInfos)
            if ok == 2 {
                <-c
                logger.Info().Msg("Inspect completed")
                for _, info := range inspectedInfos {
                    if info != nil {
                        fmt.Println(*info)
                    }
                }
            } else {
                logger.Info().Msg("Handler over capacity")
            }

            break
        }
    }

}
```

You can find more examples in **example/**

## License

This project is licensed under the Creative Commons Attribution-NonCommercial 4.0 International License.

## Contributing

Contributions are welcome! Please feel free to submit pull requests or open issues to improve the project.
If anyone knows how to make the project completelly non blocking with a different dialer, I am happy to hear from you.
