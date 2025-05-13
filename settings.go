package main

import (
	"fmt"
	"strconv"
)

type Settings struct {
	debug   bool
	review  bool
	verbose bool
}

func NewSettings() *Settings {
	return &Settings{
		debug:   false,
		review:  false,
		verbose: false,
	}
}

func (s *Settings) UpdateFromString(key string, value string) error {
	var err error

	switch key {
	case "debug":
		s.debug, err = strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid value for debug: %s", value)
		}
	case "review":
		s.review, err = strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid value for review: %s", value)
		}
	case "verbose":
		s.verbose, err = strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid value for verbose: %s", value)
		}
	default:
		return fmt.Errorf("unknown setting: %s", key)
	}

	return nil
}

func (s *Settings) Describe() string {
	return fmt.Sprintf(`Current Settings:
debug: %v
review: %v
verbose: %v
`, s.debug, s.review, s.verbose)
}
