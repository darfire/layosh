#!/bin/bash

tmux new-session -d -s "layosh" -n "main" "./layosh server -debug -session 1 $@" &&
tmux split-window -h -t "layosh:main" './layosh client -debug --mode shell -session 1' &&
tmux split-window -v -t "layosh:main" './layosh client -debug --mode llm -session 1'

tmux attach -t "layosh:main"
