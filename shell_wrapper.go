package main

import (
	"os"
	"os/exec"
	"syscall"

	pty "github.com/creack/pty"
)

type ShellWrapper struct {
	command []string

	cmd *exec.Cmd
	// we output stdout and stderr to this channel
	outputChannel chan interface{}

	// we get notified when to quit
	quitChannel chan bool
	pty         *os.File
}

type ShellExit struct {
	ExitCode int
}

func NewShellWrapper(command []string) *ShellWrapper {
	return &ShellWrapper{
		command:       command,
		outputChannel: make(chan interface{}),
		quitChannel:   make(chan bool),
	}
}

func (s *ShellWrapper) Start() error {
	// Implement the logic to start the shell command
	// This is a placeholder implementation
	c := exec.Command(s.command[0], s.command[1:]...)

	s.cmd = c

	attrs := syscall.SysProcAttr{
		// Setpgid: true,
		Setsid:  true,
		Setctty: true,
	}

	f, err := pty.StartWithAttrs(c, nil, &attrs)

	s.pty = f

	if err != nil {
		return err
	}

	stdoutChannel := make(chan []byte)

	go func() {
		for {
			buf := make([]byte, 1024)

			n, err := f.Read(buf)

			if err != nil {
				break
			}

			stdoutChannel <- buf[:n]
		}
	}()

	go func() {
		err := c.Wait()

		if err != nil {
			Error("Error waiting for command: %v", err)
		}

		Debug("Command exited with code: %d", c.ProcessState.ExitCode())

		s.outputChannel <- ShellExit{
			ExitCode: c.ProcessState.ExitCode(),
		}
	}()

	go func() {
		defer c.Process.Kill()

		for {
			select {
			case data := <-stdoutChannel:
				s.outputChannel <- data
			case <-s.quitChannel:
				return
			}
		}
	}()

	return nil
}

func (s *ShellWrapper) Stop() {
	close(s.quitChannel)
}

func (s *ShellWrapper) PushInput(input []byte) {
	if s.pty != nil {
		s.pty.Write(input)
	}
}

func (s *ShellWrapper) ResizeTerminal(width, height uint32) {
	Debug("Resizing terminal to %d x %d\n", width, height)

	if s.pty != nil {
		pty.Setsize(s.pty, &pty.Winsize{
			Cols: uint16(width),
			Rows: uint16(height),
		})
	}
}
