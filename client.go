package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/darfire/layosh/messages"

	"golang.org/x/term"
	"google.golang.org/protobuf/encoding/protodelim"
)

type Client struct {
	sessionId int
	role      messages.Role
	socket    net.Conn

	stdin          *os.File
	stdout         *os.File
	maxMessageSize uint32

	reader *bufio.Reader
	writer *bufio.Writer
}

func NewClient(
	sessionId int, role messages.Role,
	stdin *os.File, stdout *os.File) (*Client, error) {
	if sessionId == -1 {
		sessionId = os.Getpid()
	}

	return &Client{
		sessionId: sessionId,
		role:      role,
		socket:    nil,
		stdin:     stdin,
		stdout:    stdout,
	}, nil
}

func (c *Client) Register() error {
	socketPath := fmt.Sprintf("/tmp/lash-%d/default", c.sessionId)

	height, width, err := term.GetSize(int(c.stdin.Fd()))
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return err
	}

	c.socket = conn

	registrationMessage := &messages.Message{
		Type: messages.MessageType_REGISTRATION,
		Message: &messages.Message_Registration{
			Registration: &messages.RegistrationMessage{
				SessionId: uint32(c.sessionId),
				Role:      c.role,
				Width:     uint32(width),
				Height:    uint32(height),
			},
		},
	}

	c.writer = bufio.NewWriter(c.socket)
	c.reader = bufio.NewReader(c.socket)

	_, err = protodelim.MarshalTo(c.writer, registrationMessage)

	if err != nil {
		return err
	}

	err = c.writer.Flush()
	if err != nil {
		return err
	}

	var msgIn messages.Message

	err = protodelim.UnmarshalFrom(c.reader, &msgIn)
	if err != nil {
		return err
	}

	registeredMsg := msgIn.GetRegistered()

	if registeredMsg == nil {
		return fmt.Errorf("expected REGISTERED message, got %v", msgIn.Type)
	}

	if registeredMsg.MaxMessageSize > 0 {
		c.maxMessageSize = registeredMsg.MaxMessageSize
	} else {
		c.maxMessageSize = 1024
	}

	return nil
}

func (c *Client) Start() error {
	stdinFd := int(c.stdin.Fd())

	oldState, err := term.MakeRaw(stdinFd)

	if err != nil {
		return err
	}

	defer Debug("Client exiting")

	defer c.stdin.Close()

	defer term.Restore(stdinFd, oldState)

	err = c.Register()

	if err != nil {
		return err
	}

	defer c.socket.Close()

	quitChannel := make(chan bool)

	go func() {
		var message messages.Message

		for {
			err := protodelim.UnmarshalOptions{}.UnmarshalFrom(c.reader, &message)

			if err != nil {
				Error("Error reading message: %v\r\n", err)
				break
			}

			output := message.GetOutput()

			if output != nil {
				_, err = c.stdout.Write(output.Data)
				if err != nil {
					break
				}
			}

			errorMessage := message.GetError()

			if errorMessage != nil {
				break
			}
		}

		quitChannel <- true
	}()

	go func() {

		buffer := make([]byte, c.maxMessageSize/2)

		for {
			n, err := c.stdin.Read(buffer)

			if err != nil || n == 0 {
				break
			}

			if slices.Contains(buffer[:n], '\x03') {
				Info("Received Ctrl-C, exiting\r\n")
				break
			}

			if slices.Contains(buffer[:n], '\x04') {
				Info("Received Ctrl-D, exiting\r\n")
				break
			}

			message := &messages.Message{
				Type: messages.MessageType_USER_INPUT,
				Message: &messages.Message_UserInput{
					UserInput: &messages.UserInputMessage{
						Data: buffer[:n],
					},
				},
			}

			_, err = protodelim.MarshalTo(c.writer, message)

			if err != nil {
				break
			}

			err = c.writer.Flush()
			if err != nil {
				break
			}
		}

		quitChannel <- true
	}()

	sigChan := make(chan os.Signal, 1)

	signal.Notify(sigChan, syscall.SIGWINCH)

mainloop:
	for {
		select {
		case <-sigChan:
			width, height, err := term.GetSize(int(c.stdin.Fd()))

			if err != nil {
				Error("Error getting terminal size: %v\r\n", err)
				break mainloop
			}
			resizeMessage := &messages.Message{
				Type: messages.MessageType_RESIZE,
				Message: &messages.Message_Resize{
					Resize: &messages.ResizeMessage{
						Width:  uint32(width),
						Height: uint32(height),
					},
				},
			}
			_, err = protodelim.MarshalTo(c.writer, resizeMessage)

			if err != nil {
				Error("Error sending resize message: %v\r\n", err)
				break mainloop
			}

			c.writer.Flush()
		case <-quitChannel:
			break mainloop
		}
	}

	return nil
}
