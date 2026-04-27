package util

import goaway "github.com/TwiN/go-away"

func IsProfane(s string) bool {
	return goaway.IsProfane(s)
}
