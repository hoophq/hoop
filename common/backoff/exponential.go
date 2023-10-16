package backoff

import (
	"fmt"
	"time"
)

const defaulMaxBackoff = 9

var backoffFn = time.Sleep

func Errorf(format string, v ...any) error { return &backoffErr{fmt.Sprintf(format, v...)} }
func Error() error                         { return &backoffErr{} }

type backoffErr struct {
	msg string
}

func (e *backoffErr) Error() string { return e.msg }

// Exponential2x backoff the execution of fn when the attempt fail with backoff.Error().
// When the backoff attempts reaches to max, it will backoff using the last backoff value.
// Returning a non backoff error will return the execution of fn.
// Returning a nil value will reset the backoff and execution the function without any backoff
func Exponential2x(fn func(v time.Duration) error) error {
	attempt := 1
	backoffDuration := time.Duration(1)
	backoff := time.Second * backoffDuration
	for {
		err := fn(backoff)
		switch err.(type) {
		case *backoffErr:
			attempt++
			// increase exponentially until max
			if attempt <= defaulMaxBackoff {
				backoffDuration *= 2
			}
			backoff = time.Second * backoffDuration
			backoffFn(backoff)
		case nil:
			// reset
			attempt = 1
			backoffDuration = time.Duration(1)
			backoff = time.Second * backoffDuration
		default:
			// stop executing if receive a non backoff error
			return err
		}
	}
}
