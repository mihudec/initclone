package main

import (
	"fmt"
	"strings"
)

// splitArgs implements POSIX shell quoting without expanding variables or
// executing commands. Comments are literal, as with Python shlex.split.
func splitArgs(input string) ([]string, error) {
	var result []string
	var word strings.Builder
	var quote rune
	started := false
	chars := []rune(input)
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		if c == '\\' && quote != '\'' {
			if i+1 == len(chars) {
				return nil, fmt.Errorf("RCLONE_ARGS: trailing escape")
			}
			i++
			next := chars[i]
			if quote == '"' && next != '"' && next != '\\' {
				word.WriteRune(c)
			}
			word.WriteRune(next)
			started = true
		} else if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		} else if c == '\'' || c == '"' {
			quote = c
			started = true
		} else if strings.ContainsRune(" \t\r\n", c) {
			if started {
				result = append(result, word.String())
				word.Reset()
				started = false
			}
		} else {
			word.WriteRune(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("RCLONE_ARGS: unterminated quote")
	}
	if started {
		result = append(result, word.String())
	}
	return result, nil
}
