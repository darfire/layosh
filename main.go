package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/darfire/layosh/messages"
	"github.com/urfave/cli/v3"
)

type Command struct {
	args []string
}

func (c *Command) append(args ...string) *Command {
	c.args = append(c.args, args...)
	return c
}

func (c *Command) insert(index int, args ...string) *Command {
	c.args = append(c.args[:index], append(args, c.args[index:]...)...)
	return c
}

func (c *Command) String() string {
	return strings.Join(c.args, " ")
}

func (c *Command) Args() []string {
	return c.args
}

func NewCommand(args ...string) *Command {
	return &Command{
		args: args,
	}
}

func runTmuxCmd(args ...string) *exec.Cmd {
	log.Printf("Running tmux command: %v", args)

	cmd := exec.Command("tmux", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Fatalf("Error running tmux command: %v: %v", args, err)
	}

	log.Printf("Tmux command completed: %v", cmd.ProcessState)

	return cmd
}

func runServer(cmd *cli.Command) {
	command := cmd.Args().Slice()

	if len(command) == 0 {
		log.Fatal("expected command to run")
	}

	sessionId := cmd.Int("session")

	if sessionId <= 0 {
		sessionId = os.Getpid()
	}

	SetDebug(cmd.Bool("debug"))

	server, err := NewServer(command, sessionId)
	if err != nil {
		log.Fatalf("Error creating server: %v", err)
	}
	server.Start()
}

func runShellClient(cmd *cli.Command) {
	debug := cmd.Bool("debug")
	sessionId := cmd.Int("session")

	SetDebug(debug)

	client, err := NewClient(
		sessionId, messages.Role_SHELL, os.Stdin, os.Stdout)

	if err != nil {
		log.Fatalf("Error creating client: %v", err)
	}

	if err := client.Start(); err != nil {
		log.Fatalf("Error starting client: %v", err)
	}
}

func runLLMClient(cmd *cli.Command) {
	sessionId := cmd.Int("session")
	debug := cmd.Bool("debug")

	SetDebug(debug)

	client, err := NewClient(
		sessionId, messages.Role_LLM, os.Stdin, os.Stdout)
	if err != nil {
		log.Fatalf("Error creating client: %v", err)
	}
	if err := client.Start(); err != nil {
		log.Fatalf("Error starting client: %v", err)
	}
}

func runTmux(executable string, cmd *cli.Command) {
	debug := cmd.Bool("debug")
	sessionId := cmd.Int("session")
	showServer := cmd.Bool("server-output")

	command := cmd.Args().Slice()

	if len(command) == 0 {
		log.Fatal("expected command to run")
	}

	tmuxSession := fmt.Sprintf("layosh-%d", sessionId)

	serverCmd := NewCommand(
		executable, "server", "-session", fmt.Sprintf("%d", sessionId))

	shellCmd := NewCommand(
		executable, "shell", "-session", fmt.Sprintf("%d", sessionId))

	llmCmd := NewCommand(
		executable, "llm", "-session", fmt.Sprintf("%d", sessionId))

	if debug {
		serverCmd = serverCmd.append("-debug")
		shellCmd = shellCmd.append("-debug")
		llmCmd = llmCmd.append("-debug")
	}

	serverCmd = serverCmd.append(command...)

	mainWindow := fmt.Sprintf("%s:main", tmuxSession)

	if showServer {
		runTmuxCmd("new-session", "-d", "-s", tmuxSession, "-n", "main",
			serverCmd.String())

		runTmuxCmd("split-window", "-h", "-t", mainWindow,
			shellCmd.String())

		runTmuxCmd("split-window", "-v", "-t", mainWindow,
			llmCmd.String())
	} else {
		serverCmd.insert(2, "-daemon")

		args := serverCmd.Args()

		log.Printf("Running server command: %v", args)

		cmd := exec.Command(args[0], args[1:]...)

		cmd.Start()

		runTmuxCmd("new-session", "-d", "-s", tmuxSession, "-n", "main",
			shellCmd.String())

		runTmuxCmd("split-window", "-h", "-t", mainWindow,
			llmCmd.String())
	}

	runTmuxCmd("attach", "-t", fmt.Sprintf("%s:main", tmuxSession))
}

func main() {
	executable := os.Args[0]

	cmd := &cli.Command{
		Name:  "layosh",
		Usage: "layosh is a shell client for LLMs",
		Commands: []*cli.Command{
			{
				Name:  "server",
				Usage: "start the server",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "debug",
						Usage: "enable debug mode",
					},
					&cli.BoolFlag{
						Name:  "daemon",
						Usage: "run in the background",
					},
					&cli.IntFlag{
						Name:  "session",
						Usage: "session id",
					},
					&cli.StringSliceFlag{
						Name:  "command",
						Usage: "command to run",
					},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					runServer(c)
					return nil
				},
			},
			{
				Name:  "shell",
				Usage: "start the shell client",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "debug",
						Usage: "enable debug mode",
					},
					&cli.IntFlag{
						Name:  "session",
						Usage: "session id",
					},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					runShellClient(c)
					return nil
				},
			},
			{
				Name:  "llm",
				Usage: "start the LLM client",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "debug",
						Usage: "enable debug mode",
					},
					&cli.IntFlag{
						Name:  "session",
						Usage: "session id",
					},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					runLLMClient(c)
					return nil
				},
			},
			{
				Name:  "tmux",
				Usage: "start tmux session",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "debug",
						Usage: "enable debug mode",
					},
					&cli.IntFlag{
						Name:  "session",
						Usage: "session id",
					},
					&cli.BoolFlag{
						Name:  "server-output",
						Usage: "show server output(for debugging)",
					},
					&cli.StringSliceFlag{
						Name:  "command",
						Usage: "command to run",
					},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					runTmux(executable, c)
					return nil
				},
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
