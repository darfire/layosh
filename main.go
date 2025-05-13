package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/darfire/layosh/messages"
)

func main() {
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)

	serverSessionId := serverCmd.Int("session", -1, "Session ID")

	serverDebug := serverCmd.Bool("debug", false, "Enable debug mode")

	clientCmd := flag.NewFlagSet("client", flag.ExitOnError)

	clientSessionId := clientCmd.Int("session", -1, "Session ID")

	clientDebug := clientCmd.Bool("debug", false, "Enable debug mode")

	mode := clientCmd.String("mode", "shell", "Mode to run in")

	if len(os.Args) < 2 {
		log.Fatal("expected 'server' or 'client' subcommands")
	}

	switch os.Args[1] {
	case "server":
		serverCmd.Parse(os.Args[2:])

		SetDebug(*serverDebug)

		command := serverCmd.Args()

		Debug("Server command: %v", command)

		server, err := NewServer(command, *serverSessionId)

		if err != nil {
			log.Fatalf("Error creating server: %v", err)
		}

		server.Start()

	case "client":
		clientCmd.Parse(os.Args[2:])

		SetDebug(*clientDebug)

		if *mode != "shell" && *mode != "llm" {
			log.Fatal("Invalid mode. Use 'shell' or 'llm'.")
		}

		role, err := roleFromString(*mode)

		if err != nil {
			log.Fatalf("Error parsing role: %v", err)
		}

		client, err := NewClient(
			*clientSessionId, role, os.Stdin, os.Stdout)

		if err != nil {
			log.Fatalf("Error creating client: %v", err)
		}

		if err := client.Start(); err != nil {
			log.Fatalf("Error starting client: %v", err)
		}
	}
}

func roleFromString(role string) (messages.Role, error) {
	switch role {
	case "shell":
		return messages.Role_SHELL, nil
	case "llm":
		return messages.Role_LLM, nil
	default:
		return 0, fmt.Errorf("invalid role: %s", role)
	}
}
