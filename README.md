# LayoSH, LLM-assisted shell interaction

Layosh is a shell command line interface that uses LLMs to assist users in executing shell commands. It is designed to help users who may not be familiar with the command line or who want to speed up their workflow.

It provides a dual interface: direct interaction with the shell command line and a chat interface to a Large Language Model (LLM). The LLM can assist users in generating shell commands, explaining commands, and providing help with shell scripting.

## Features
- **LLM-assisted command generation**: Generate shell commands based on natural language input.
- **Command explanation**: Get explanations of shell commands and their options.
- **Review before execution**: Review generated commands before executing them
- **Works with any REPL environment**: Can be used with any REPL command, like: bash, python / ipython, mongo, psql, etc.
- **Bring your own LLM**: Use any LLM of your choice: cloud-based (Gemini, OpenAI, etc.) or local (Llama, Gemma, etc.), through Ollama.
- **Customizable display**: Since LayoSH does not provide a GUI, you can forward the terminal output to any compatible GUI, like Tmux or a web-based terminal.

## Architecture
Inspired by Tmux, LayoSH is a server that wraps both the shell command and the LLM interaction.

To interact with the shell and the LLM, LayoSH users connect to the server using the **layosh shell/llm** sub-commands. These forward the input/output between the local terminal and the LayoSH server.

The modular design allows for easy integration with different pseudo-terminals, like Tmux, Xterm or a web-based terminal.

## Installation

Clone the repository:
```bash
git clone github.com/darfire/layosh
```

Build the project:
```bash
cd layosh
go build
```

## Running LayoSH

Run the tmux wrapper:
```bash
./layosh tmux -session 1 bash
```

Run just the LayoSH server:
```bash
./layosh server -session 1 bash
```

Connect to the shell:
```bash
./layosh shell -session 1
```

Connect to the LLM:
```bash
./layosh llm -session 1
```

## Roadmap
- [X] Tmux wrapper
- [ ] Add support for more LLMs: openai, ollama, etc.
- [ ] Customize the LLM prompt for major shells
- [ ] Add web-server support
- [ ] Review mode: review and/or edit the command before executing it
- [ ] Support multi-step / chain of thought execution