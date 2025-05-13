package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/darfire/layosh/messages"

	"google.golang.org/protobuf/encoding/protodelim"
	"google.golang.org/protobuf/proto"
)

type Server struct {
	command      []string
	listenSocket net.Listener

	shellSocket net.Conn
	llmSocket   net.Conn

	shellWriter *bufio.Writer
	llmWriter   *bufio.Writer

	sessionId uint32

	shellWrapper *ShellWrapper
	llmWrapper   *LLMWrapper

	shellChannel chan interface{}
	llmChannel   chan interface{}

	lastShellLine []byte
	lastLLMLine   []byte

	isClosed bool
}

type Size struct {
	Width  uint32
	Height uint32
}

func NewServer(command []string, sessionId int) (*Server, error) {
	if sessionId == -1 {
		sessionId = os.Getpid()
	}

	Info("Creating server with session ID %d", sessionId)

	// make a unix socket at /tmp/lash-${sessionId}/default

	socketPath := fmt.Sprintf("/tmp/lash-%d/default", sessionId)

	// remove the socket if it exists
	if _, err := os.Stat(socketPath); err == nil {
		os.Remove(socketPath)
	}

	// create the directory if it doesn't exist
	dirPath := filepath.Dir(socketPath)
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		err := os.MkdirAll(dirPath, 0755)
		if err != nil {
			return nil, err
		}
	}

	listenSocket, err := net.Listen("unix", socketPath)

	if err != nil {
		return nil, err
	}

	llmWrapper, err := NewLLMWrapper(command)

	if err != nil {
		return nil, err
	}

	return &Server{
		command:      command,
		listenSocket: listenSocket,
		sessionId:    uint32(sessionId),

		shellSocket: nil,
		llmSocket:   nil,

		shellWrapper: NewShellWrapper(command),
		llmWrapper:   llmWrapper,

		shellChannel: make(chan interface{}),
		llmChannel:   make(chan interface{}),
	}, nil
}

func (s *Server) Start() {
	defer s.Stop()

	connectionChannel := make(chan net.Conn)

	s.shellWrapper.Start()
	s.llmWrapper.Start()

	signal.Reset(os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for {
			conn, err := s.listenSocket.Accept()

			if err == net.ErrClosed {
				Info("Listener closed")
				break
			}

			if err != nil {
				continue
			}

			connectionChannel <- conn
		}

		close(connectionChannel)
	}()

	sigChannel := make(chan os.Signal, 1)
	signal.Notify(sigChannel, syscall.SIGINT, syscall.SIGTERM)

	for !s.isClosed {
		select {
		case conn, more := <-connectionChannel:
			if !more {
				Info("Connection channel closed")
				return
			}
			go s.handleConnection(conn)
		case msg := <-s.shellWrapper.outputChannel:
			s.handleShellOutput(msg)
		case msg := <-s.llmWrapper.outputChannel:
			s.handleLLMOutput(msg)
		case data := <-s.shellChannel:
			s.handleShellInput(data)
		case data := <-s.llmChannel:
			s.handleLLMInput(data)
		case sig := <-sigChannel:
			Debug("Received signal: %v", sig)
			return
		}
	}

	log.Printf("Server closed")
}

func (s *Server) handleShellOutput(msg interface{}) {
	switch msg.(type) {
	case ShellExit:
		exit := msg.(ShellExit)
		s.outputToShell([]byte(fmt.Sprintf("Shell exited with code %d\r\n", exit.ExitCode)))
		s.isClosed = true
	case []byte:
		data := msg.([]byte)
		Debug("Received shell output: %d bytes", len(data))
		s.outputToShell(data)
		s.llmWrapper.AddShellOutput(data)
	}
}

func (s *Server) handleLLMOutput(msg interface{}) {
	switch msg.(type) {
	case string:
		Debug("Received LLM output as string: %d bytes", len(msg.(string)))
		s.outputToLLM([]byte(msg.(string)))
	case []byte:
		Debug("Received LLM output as bytes: %d bytes", len(msg.([]byte)))
		s.outputToLLM(msg.([]byte))
	case LLMResponse:
		response := msg.(LLMResponse)
		s.shellWrapper.PushInput([]byte(response.command + "\r\n"))
		s.outputToLLM([]byte("\r" + response.describe()))
		s.llmWrapper.AddLLMInput([]byte("\r\n"))
	case QuitCommand:
		s.isClosed = true
	}
}

func (s *Server) handleShellInput(msg interface{}) {
	switch msg.(type) {
	case []byte:
		data := msg.([]byte)
		Debug("Received shell input: %d bytes", len(data))
		s.llmWrapper.AddShellInput(data)
		s.shellWrapper.PushInput(data)
	case Size:
		size := msg.(Size)
		Debug("Received shell resize: %d x %d", size.Width, size.Height)
		s.shellWrapper.ResizeTerminal(size.Width, size.Height)
	}
}

func (s *Server) handleLLMInput(msg interface{}) {
	switch msg.(type) {
	case []byte:
		data := msg.([]byte)
		Debug("Received LLM input: %d bytes", len(data))
		s.llmWrapper.AddLLMInput(data)
	case Size:
		size := msg.(Size)
		Debug("Received LLM resize: %d x %d", size.Width, size.Height)
		s.llmWrapper.ResizeTerminal(size.Width, size.Height)
	}
}

func (s *Server) outputToConn(data []byte, writer *bufio.Writer) {
	if writer == nil {
		return
	}

	message := &messages.Message{
		Type: messages.MessageType_OUTPUT,
		Message: &messages.Message_Output{
			Output: &messages.OutputMessage{
				Data: data,
			},
		},
	}

	_, err := protodelim.MarshalTo(writer, message)
	if err != nil {
		Error("Error marshalling message: %v", err)
		return
	}

	err = writer.Flush()
	if err != nil {
		Error("Error flushing data: %v", err)
		return
	}
}

func (s *Server) outputToShell(data []byte) {
	s.lastShellLine = keepLastLine(s.lastLLMLine, data)
	s.outputToConn(data, s.shellWriter)
}

func (s *Server) outputToLLM(data []byte) {
	s.lastLLMLine = keepLastLine(s.lastLLMLine, data)
	s.outputToConn(data, s.llmWriter)
}

func getLastLine(data []byte) (int, []byte) {
	// return number of lines in data and the last line
	// the last line is the current line after the last \n

	n := 0
	last := []byte{}

	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			n++
			last = data[i+1:]
		}
	}

	if n == 0 {
		return 0, data
	} else {
		return n, last
	}
}

func keepLastLine(lastLine []byte, data []byte) []byte {
	n, last := getLastLine(data)
	if n == 0 {
		return slices.Concat(lastLine, data)
	} else {
		return last
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Unmarshal protobuf command
	var message messages.Message

	reader := bufio.NewReader(conn)

	Debug("Reading registration message from connection")

	err := protodelim.UnmarshalFrom(reader, &message)

	if err != nil {
		Error("Error unmarshalling command: %v", err)
		return
	}

	Debug("Received message of type %v, size = %d", message.Type, proto.Size(&message))

	registration := message.GetRegistration()

	if registration == nil {
		Error("Expected registration message, got %v", message.Type)
		return
	}

	sessionId := registration.SessionId

	role := registration.Role

	if sessionId != s.sessionId {
		Error("Session ID mismatch: expected %d, got %d",
			s.sessionId, sessionId)
		return
	}

	Debug("Session ID: %d, Role: %v, size = %d x %d",
		sessionId, role, registration.Width, registration.Height)

	var channel chan interface{}

	writer := bufio.NewWriter(conn)

	var lastLine []byte

	if role == messages.Role_SHELL && s.shellSocket == nil {
		defer func() {
			s.shellSocket = nil
			s.shellWriter = nil
		}()

		s.shellSocket = conn
		s.shellWriter = writer
		channel = s.shellChannel
		lastLine = s.lastShellLine

		s.shellWrapper.ResizeTerminal(registration.Width, registration.Height)
	} else if role == messages.Role_LLM && s.llmSocket == nil {
		defer func() {
			s.llmSocket = nil
			s.llmWriter = nil
		}()

		s.llmSocket = conn
		s.llmWriter = writer
		channel = s.llmChannel
		lastLine = s.lastLLMLine
	} else {
		Error("Unknown role: %v", role)
		return
	}

	response := &messages.Message{
		Type: messages.MessageType_REGISTERED,
		Message: &messages.Message_Registered{
			Registered: &messages.RegisteredMessage{
				MaxMessageSize: 1024,
			},
		},
	}

	_, err = protodelim.MarshalTo(writer, response)
	if err != nil {
		Error("Error marshalling response: %v", err)
		return
	}

	err = writer.Flush()
	if err != nil {
		Error("Error flushing data: %v", err)
		return
	}

	lastLineMessage := &messages.Message{
		Type: messages.MessageType_OUTPUT,
		Message: &messages.Message_Output{
			Output: &messages.OutputMessage{
				Data: lastLine,
			},
		},
	}

	_, err = protodelim.MarshalTo(writer, lastLineMessage)
	if err != nil {
		Error("Error marshalling last line message: %v", err)
		return
	}

	err = writer.Flush()
	if err != nil {
		Error("Error flushing data: %v", err)
		return
	}

	s.runConnection(reader, channel)
}

func (s *Server) runConnection(reader *bufio.Reader, channel chan interface{}) {
	for {
		message := &messages.Message{}

		err := protodelim.UnmarshalFrom(reader, message)

		if err != nil {
			Error("Error unmarshalling message: %v", err)
			return
		}

		userInput := message.GetUserInput()

		if userInput != nil {
			channel <- userInput.Data
		}

		resize := message.GetResize()

		if resize != nil {
			channel <- Size{
				Width:  resize.Width,
				Height: resize.Height,
			}
		}
	}
}

func (s *Server) Stop() {
	log.Printf("Stopping server")
	if s.shellSocket != nil {
		s.shellSocket.Close()
	}

	if s.llmSocket != nil {
		s.llmSocket.Close()
	}

	if s.listenSocket != nil {
		s.listenSocket.Close()
	}

	s.shellWrapper.Stop()
	s.llmWrapper.Stop()

	os.Remove(fmt.Sprintf("/tmp/lash-%d/default", s.sessionId))
}
