package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/chzyer/readline"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/google/uuid"
)

type LLMWrapper struct {
	shellCommand   []string
	outputChannel  chan interface{}
	quitChannel    chan bool
	requestChannel chan LLMRequest

	shellHistory bytes.Buffer
	llmHistory   bytes.Buffer

	settings Settings

	readerOut *io.PipeReader
	writerIn  *io.PipeWriter
	readline  *readline.Instance
}

type LLMRequest struct {
	shellHistory string
	llmHistory   string
	request      string
	id           string
}

type LLMResponse struct {
	command    string
	commentary string
}

type LLMError struct {
	err error
}

func (e LLMError) Error() string {
	return e.err.Error()
}

type LLMSuggestion struct {
	Command    string `json:"command"`
	Commentary string `json:"commentary"`
}

func NewLLMWrapper(shellCommand []string) (*LLMWrapper, error) {
	readerIn, writerIn := io.Pipe()
	readerOut, writerOut := io.Pipe()

	readline, err := readline.NewEx(&readline.Config{
		Prompt:              "\x1b[32mLash LLM\x1b[0m> ",
		Stdin:               readerIn,
		Stdout:              writerOut,
		Stderr:              writerOut,
		StdinWriter:         writerIn,
		ForceUseInteractive: true,
		FuncMakeRaw:         func() error { return nil },
		FuncExitRaw:         func() error { return nil },
	})

	if err != nil {
		return nil, err
	}

	return &LLMWrapper{
		shellCommand:   shellCommand,
		outputChannel:  make(chan interface{}),
		quitChannel:    make(chan bool),
		requestChannel: make(chan LLMRequest),

		shellHistory: bytes.Buffer{},
		llmHistory:   bytes.Buffer{},

		writerIn:  writerIn,
		readerOut: readerOut,

		readline: readline,
	}, nil
}

const (
	PROMPT_TEMPLATE = `
You are a shell command suggestion engine. Given the following shell history and LLM history, suggest a shell command that is relevant to the user's request.
COMMAND: %COMMAND%
SHELL HISTORY BELOW:
%SHELL_HISTORY%
LLM HISTORY BELOW:
%LLM_HISTORY%
USER REQUEST: %USER_REQUEST%
`
)

func replacePlaceholder(prompt, placeholder, value string) string {
	return strings.ReplaceAll(prompt, placeholder, value)
}

func (l *LLMWrapper) makePrompt(request LLMRequest) string {
	prompt := PROMPT_TEMPLATE
	prompt = replacePlaceholder(prompt, "%COMMAND%", strings.Join(l.shellCommand, " "))
	prompt = replacePlaceholder(prompt, "%SHELL_HISTORY%", request.shellHistory)
	prompt = replacePlaceholder(prompt, "%LLM_HISTORY%", request.shellHistory)
	prompt = replacePlaceholder(prompt, "%USER_REQUEST%", request.request)
	return prompt
}

func (l *LLMWrapper) Start() {
	ctx := context.Background()

	g, err := genkit.Init(ctx,
		genkit.WithPlugins(&googlegenai.GoogleAI{}),
		genkit.WithDefaultModel("googleai/gemini-2.0-flash"),
	)

	if err != nil {
		panic(err)
	}

	l.readline.CaptureExitSignal()

	getSuggestionFlow := genkit.DefineFlow(
		g,
		"ShellSuggestion",
		func(ctx context.Context, request LLMRequest) (LLMResponse, error) {
			prompt := l.makePrompt(request)

			suggestion, _, err := genkit.GenerateData[LLMSuggestion](
				ctx, g, ai.WithPrompt(prompt))

			if err != nil {
				Error("Error generating suggestion: %v\n", err)
				return LLMResponse{}, err
			}

			return LLMResponse{
				command:    suggestion.Command,
				commentary: suggestion.Commentary,
			}, nil
		},
	)

	go func() {
		for {
			line, err := l.readline.Readline()

			if err != nil {
				Error("Error reading line: %v\n", err)
				l.outputChannel <- QuitCommand{}
				break
			}

			l.handleLine(line)
		}

		log.Printf("LLMWrapper: readline closed\n")
	}()

	go func() {
		for {
			buf := make([]byte, 1024)

			n, err := l.readerOut.Read(buf)

			if err != nil {
				break
			}

			l.outputChannel <- buf[:n]
		}

		log.Printf("LLMWrapper: readerOut closed\n")
	}()

	go func() {
	mainloop:
		for {
			select {
			case request := <-l.requestChannel:
				response, err := getSuggestionFlow.Run(ctx, request)

				if err != nil {
					l.outputChannel <- err
					continue
				}

				l.outputChannel <- response

			case <-l.quitChannel:
				break mainloop
			}
		}

		log.Printf("LLMWrapper: channel loop closed\n")
	}()
}

func (l *LLMWrapper) Stop() {
	l.writerIn.Close()
	l.readline.Close()
	close(l.quitChannel)
}

func (l *LLMWrapper) AddShellOutput(data []byte) {
	l.shellHistory.Write(data)
}

func (l *LLMWrapper) AddShellInput(data []byte) {
	l.shellHistory.Write(data)
}

func (l *LLMWrapper) AddLLMInput(data []byte) {
	Debug("Adding LLM input: %d bytes\n", len(data))
	l.writerIn.Write(data)
}

func (l *LLMWrapper) outputToTerminal(data string) {
	Debug("Outputting to terminal: %d bytes\n", len(data))
	l.outputChannel <- "\x1b[34m" + data + "\x1b[0m"
}

func (l *LLMWrapper) handleLine(line string) error {
	l.llmHistory.WriteString(line + "\n")

	command, err := parseCommand(line)

	if err != nil {
		l.outputToTerminal(fmt.Sprintf("Error: %s\r\n", err.Error()))
		return err
	}

	if command != nil {
		l.handleCommand(command)
	} else {
		// it's an LLM request
		Debug("Handling LLM request: '%s'\n", line)

		line = strings.TrimSpace(line)

		if len(line) == 0 {
			return nil
		}

		requestId := uuid.New().String()

		request := LLMRequest{
			shellHistory: l.shellHistory.String(),
			llmHistory:   l.llmHistory.String(),
			request:      line,
			id:           requestId,
		}

		l.requestChannel <- request
	}

	return nil
}

type QuitCommand struct{}

type ClearHistoryCommand struct{}
type UpdateSettingsCommand struct {
	key   string
	value string
}
type HelpCommand struct{}
type ShowSettingsCommand struct{}

func (c QuitCommand) String() string {
	return "QuitCommand"
}

func (c ClearHistoryCommand) String() string {
	return "ClearHistoryCommand"
}

func (c UpdateSettingsCommand) String() string {
	return fmt.Sprintf("UpdateSettingsCommand{key: %s, value: %s}", c.key, c.value)
}

func (c HelpCommand) String() string {
	return "HelpCommand"
}

func (c ShowSettingsCommand) String() string {
	return "ShowSettingsCommand"
}

func (l *LLMWrapper) handleCommand(command interface{}) {
	Debug("Handling command: %v\n", command)
	switch cmd := command.(type) {
	case QuitCommand:
		l.outputChannel <- cmd
	case ClearHistoryCommand:
		l.shellHistory.Reset()
		l.llmHistory.Reset()
	case UpdateSettingsCommand:
		l.settings.UpdateFromString(cmd.key, cmd.value)
	case HelpCommand:
		l.outputToTerminal(adjustNewlines(l.generateHelpMessage()))
	case ShowSettingsCommand:
		l.outputChannel <- adjustNewlines(l.settings.Describe())
	default:
		Error("Unknown LLM command: %v\n", cmd)
	}
}

func (l *LLMWrapper) GetOutputChannel() chan interface{} {
	return l.outputChannel
}

func (l *LLMWrapper) generateHelpMessage() string {
	return `Lash LLM Help
Lash LLM is a shell command suggestion engine. It uses the LLM to suggest shell commands based on the user's request and the shell history.
Commands:
- /quit: Quit the LLM
- /clear: Clear the shell and LLM history
- /set <key> <value>: Set a configuration key to a value
- /help: Show this help message
- /settings: Show the current settings
- /show: Show the current shell command
`
}

func parseCommand(line string) (interface{}, error) {
	if len(line) < 2 {
		return nil, nil
	}

	if line[0] == '/' {
		trimmedLine := strings.TrimSpace(line)[1:]

		if trimmedLine == "q" || trimmedLine == "quit" {
			return QuitCommand{}, nil
		} else if trimmedLine == "h" || trimmedLine == "help" {
			return HelpCommand{}, nil
		} else if trimmedLine == "c" || trimmedLine == "clear" {
			return ClearHistoryCommand{}, nil
		} else if strings.HasPrefix(trimmedLine, "set ") {
			parts := strings.SplitN(trimmedLine[4:], " ", 2)
			if len(parts) != 2 {
				return nil, LLMError{err: fmt.Errorf("invalid set command")}
			}
			return UpdateSettingsCommand{key: parts[0], value: parts[1]}, nil
		} else if trimmedLine == "settings" {
			return ShowSettingsCommand{}, nil
		}

		return nil, LLMError{err: fmt.Errorf("unknown command: %s", trimmedLine)}
	}

	return nil, nil
}

func (r LLMResponse) describe() string {
	return fmt.Sprintf("\x1b[34mCommand: %s\r\nExplanation: %s\r\n\x1b[0m", r.command, r.commentary)
}

func adjustNewlines(s string) string {
	lines := strings.Split(s, "\n")

	for i, line := range lines {
		lines[i] = strings.TrimRight(line, "\r\n")
	}

	output := strings.Join(lines, "\r\n")

	return output
}

func (l *LLMWrapper) ResizeTerminal(width, height uint32) {
	// we don't handle resizing in the LLM wrapper
}
