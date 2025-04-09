
# CS2-Inspect

A Go-based tool for inspecting and retrieving information about CS2 (Counter-Strike 2) items.

## Overview

CS2-Inspect is a library that allows you to inspect and retrieve detailed information about Counter-Strike 2 items, float value, paintseed, stickers, and keychains. It communicates with the CS2 game coordinator to fetch accurate and up-to-date item data.

## Features

- Inspect CS2 items using various identifiers
- Retrieve detailed item information including wear values, stickers, and patterns
- Authentication handling for game coordinator requests
- Metrics logging for monitoring
- Token-based authentication system
- Support for custom authenticators

## Installation

```bash
go get github.com/yourname/cs2-inspect
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