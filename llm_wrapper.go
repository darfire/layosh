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
	"github.com/firebase/genkit/go/core"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"github.com/firebase/genkit/go/plugins/ollama"
	"github.com/google/uuid"
)

type LLMWrapper struct {
	shellCommand  []string
	outputChannel chan interface{}
	inputChannel  chan interface{}
	quitChannel   chan bool

	shellHistory bytes.Buffer
	llmHistory   bytes.Buffer

	settings *Settings

	readerOut *io.PipeReader
	writerIn  *io.PipeWriter
	readline  *readline.Instance

	modelConfig ModelConfig

	genkit *genkit.Genkit
	model  ai.Model

	flow *core.Flow[LLMRequest, LLMResponse, struct{}]

	context context.Context
}

type AddShellHistoryCommand struct {
	data []byte
}

type AddLLMHistoryCommand struct {
	data []byte
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

type ModelConfig struct {
	Provider string

	ModelName string

	AuthKey string

	// ollama-specific
	OllamaAddress string
}

func NewModelConfig() ModelConfig {
	return ModelConfig{
		Provider:  "googleai",
		ModelName: "gemini-2.0",
	}
}

func NewLLMWrapper(modelConfig ModelConfig, options ...func(*LLMWrapper)) (*LLMWrapper, error) {
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

	ctx := context.Background()

	gk, model, err := MakeGenkitAndModel(modelConfig, ctx)

	if err != nil {
		return nil, err
	}

	l := &LLMWrapper{
		outputChannel: make(chan interface{}),
		inputChannel:  make(chan interface{}),
		quitChannel:   make(chan bool),

		shellHistory: bytes.Buffer{},
		llmHistory:   bytes.Buffer{},

		writerIn:  writerIn,
		readerOut: readerOut,

		readline: readline,

		genkit:      gk,
		model:       model,
		modelConfig: modelConfig,
		context:     ctx,

		settings: NewSettings(),
	}

	flow := genkit.DefineFlow(
		gk,
		"ShellSuggestion",
		func(ctx context.Context, request LLMRequest) (LLMResponse, error) {
			Debug("LLMWrapper: generating suggestion for request: %s\n", request.request)

			prompt := l.makePrompt(request)

			suggestion, _, err := genkit.GenerateData[LLMSuggestion](
				ctx, gk, ai.WithModel(model), ai.WithPrompt(prompt))

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

	l.flow = flow

	for _, option := range options {
		option(l)
	}

	return l, err
}

func WithCommand(command []string) func(*LLMWrapper) {
	return func(l *LLMWrapper) {
		l.shellCommand = command
	}
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

func WithModelConfig(modelConfig ModelConfig) func(*LLMWrapper) {
	return func(l *LLMWrapper) {
		l.modelConfig = modelConfig
	}
}

func (c *ModelConfig) Plugins() []genkit.Plugin {
	switch c.Provider {
	case "googleai":
		return []genkit.Plugin{&googlegenai.GoogleAI{}}
	case "ollama":
		return []genkit.Plugin{&ollama.Ollama{
			ServerAddress: c.OllamaAddress,
		}}
	default:
		return nil
	}
}

func MakeGenkitAndModel(modelConfig ModelConfig, ctx context.Context) (*genkit.Genkit, ai.Model, error) {
	plugins := modelConfig.Plugins()

	genkit, err := genkit.Init(ctx,
		genkit.WithPlugins(plugins...),
	)

	if err != nil {
		return nil, nil, err
	}

	var model ai.Model

	ollamaClient := ollama.Ollama{
		ServerAddress: modelConfig.OllamaAddress,
	}

	err = ollamaClient.Init(ctx, genkit)

	if err != nil {
		return nil, nil, err
	}

	switch modelConfig.Provider {
	case "googleai":
		model = googlegenai.GoogleAIModel(genkit, modelConfig.ModelName)
	case "ollama":
		model = ollamaClient.DefineModel(
			genkit,
			ollama.ModelDefinition{
				Name: modelConfig.ModelName,
				Type: "chat",
			},
			nil,
		)

		Debug("Ollama model: %s, %v\n", modelConfig.ModelName, model)
	default:
		return nil, nil, fmt.Errorf("unknown model provider: %s", modelConfig.Provider)
	}

	return genkit, model, nil
}

func (l *LLMWrapper) Start() {
	l.readline.CaptureExitSignal()

	lineChannel := make(chan string)

	go func() {
		for {
			line, err := l.readline.Readline()

			if err != nil {
				Error("Error reading line: %v\n", err)
				l.outputChannel <- QuitCommand{}
				break
			}

			Debug("LLMWrapper: read line: %s\n", line)

			lineChannel <- line
		}

		Debug("LLMWrapper: readline closed\n")
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

		Debug("LLMWrapper: readerOut closed\n")
	}()

	go func() {
	mainloop:
		for {
			select {
			case line := <-lineChannel:
				Debug("LLMWrapper: got line from readline: %s\n", line)
				err := l.handleLine(line)

				if err != nil {
					l.outputChannel <- err
					continue
				}

			case command := <-l.inputChannel:
				switch cmd := command.(type) {
				case AddShellHistoryCommand:
					l.shellHistory.Write(cmd.data)
					l.shellHistory.Write([]byte("\n"))
				}

			case <-l.quitChannel:
				break mainloop
			}
		}

		Debug("LLMWrapper: channel loop closed\n")
	}()
}

func (l *LLMWrapper) Stop() {
	l.writerIn.Close()
	l.readline.Close()
	close(l.quitChannel)
}

func (l *LLMWrapper) AddShellOutput(data []byte) {
	Debug("Adding shell output: %d bytes\n", len(data))
	l.inputChannel <- AddShellHistoryCommand{data: data}
}

func (l *LLMWrapper) AddShellInput(data []byte) {
	Debug("Adding shell input: %d bytes\n", len(data))
	l.inputChannel <- AddShellHistoryCommand{data: data}
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
	log.Printf("Handling line: %s\n", line)

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

		l.handleLLMRequest(request)
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

func (l *LLMWrapper) handleLLMRequest(request LLMRequest) {
	log.Printf("Handling LLM request: %s\n", request.request)
	response, err := l.flow.Run(l.context, request)

	if err != nil {
		l.outputChannel <- err
		return
	}

	l.outputChannel <- response
}
