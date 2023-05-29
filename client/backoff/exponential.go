package backoff

import (
	"fmt"
	"time"

	"github.com/runopsio/hoop/common/log"
)

const defaulMaxBackoff = 9

var backoffFn = time.Sleep

func Errorf(format string, v ...any) error { return &backoffErr{fmt.Sprintf(format, v...)} }

type backoffErr struct {
	msg string
}

func (e *backoffErr) Error() string { return e.msg }

// Exponential2x backoff the execution of fn when the attempt fail with backoff.Error().
// When the backoff attempts reaches to max, it will backoff using the last backoff value.
// Returning a non backoff error will return the execution of fn.
func Exponential2x(fn func() error) error {
	attempt := 1
	backoffDuration := time.Duration(1)
	for {
		err := fn()
		switch err.(type) {
		case *backoffErr:
			attempt++
			// increase exponentially until max
			if attempt <= defaulMaxBackoff {
				backoffDuration *= 2
			}
			backoff := time.Second * backoffDuration
			log.With("backoff", backoff.String()).Info(err)
			backoffFn(backoff)
		case nil:
		default:
			// stop executing if receive a non backoff error
			return err
		}
	}
}
