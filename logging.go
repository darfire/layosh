package main

import "log"

const (
	DEBUG = iota
	INFO
	WARN
	ERROR
)

var logLevel = INFO

func SetLogLevel(level int) {
	logLevel = level
}

func SetDebug(debug bool) {
	if debug {
		logLevel = DEBUG
	} else {
		logLevel = INFO
	}
}

func Log(level int, format string, args ...interface{}) {
	if level < logLevel {
		return
	}

	switch level {
	case DEBUG:
		log.Printf("[DEBUG] "+format, args...)
	case INFO:
		log.Printf("[INFO] "+format, args...)
	case WARN:
		log.Printf("[WARN] "+format, args...)
	case ERROR:
		log.Printf("[ERROR] "+format, args...)
	default:
		log.Printf("[UNKNOWN] "+format, args...)
	}
}

func Debug(format string, args ...interface{}) {
	Log(DEBUG, format, args...)
}

func Info(format string, args ...interface{}) {
	Log(INFO, format, args...)
}

func Warn(format string, args ...interface{}) {
	Log(WARN, format, args...)
}

func Error(format string, args ...interface{}) {
	Log(ERROR, format, args...)
}
