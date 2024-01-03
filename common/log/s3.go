package log

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap/zapcore"
)

const (
	maxS3FlushSize       = 1024 * 200 // 200KB
	flushTimeoutDuration = time.Second * 4
	flushTickerDuration  = time.Minute * 2
)

type S3LogWriter struct {
	appName     string
	environment string
	keySuffix   string
	mu          sync.RWMutex
	flushed     bool
	doneC       chan bool
	s3Client    *s3.Client

	s3Buf        string
	flushAttempt int
}

// NewS3LoggerWriter creates a new S3LogWriter, if environment is empty the logger will be disabled
func NewS3LoggerWriter(appName, environment, keySuffix string) *S3LogWriter {
	return &S3LogWriter{
		appName:     appName,
		environment: environment,
		keySuffix:   keySuffix,
		mu:          sync.RWMutex{},
		doneC:       make(chan bool),
	}
}

func (w *S3LogWriter) appendLogBuffer(b []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.s3Buf += string(b)
}

func (w *S3LogWriter) flushLogBuffer() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.s3Buf != "" {
		timeoutCtx, cancelFn := context.WithTimeout(context.Background(), flushTimeoutDuration)
		defer cancelFn()
		_, err := w.s3Client.PutObject(timeoutCtx, &s3.PutObjectInput{
			Bucket:             aws.String("hooplogs"),
			Key:                w.blobKey(),
			Body:               bytes.NewBufferString(w.s3Buf),
			ContentDisposition: aws.String("attachment"),
			ContentType:        aws.String("text/plain"),
			ContentLength:      aws.Int64(int64(len(w.s3Buf))),
		})
		if err == nil {
			w.s3Buf = ""
			return
		}
		// attempt to not let the buffer grow too much
		// discard the buffer if it fails to flush 3 times in a row
		w.flushAttempt++
		if w.flushAttempt > 3 {
			w.s3Buf = ""
			w.flushAttempt = 0
		}
	}
}

// Write it's a fake zap logger that writes the contents of p into the sink channel
func (w *S3LogWriter) Write(p []byte) (n int, err error) {
	if w.flushed || w.environment == "" {
		return
	}
	w.appendLogBuffer(p)
	if len(w.s3Buf) >= maxS3FlushSize {
		w.flushLogBuffer()
	}
	return len(p), nil
}

// Flush the buffer to s3.
func (w *S3LogWriter) Flush() (err error) {
	if w.flushed || w.environment == "" {
		return
	}
	close(w.doneC)
	w.flushed = true
	w.flushLogBuffer()
	return
}

// Init will make any log call to flush logs to s3. It will also reasign global functions.
// It's up to the caller to flush the remainder buffer to s3.
//
// Make sure to call this function in the beginning of the main function,
// it's not thread safe because it reasigns global functions by constructing a new logger.
func (w *S3LogWriter) Init() error {
	if w.environment == "" {
		Infof("s3 logger disabled")
		return nil
	}
	s3Client, err := newS3Client()
	if err != nil {
		return err
	}
	w.s3Client = s3Client
	ticker := time.NewTicker(flushTickerDuration)
	go func() {
		for {
			select {
			case <-w.doneC:
				return
			case <-ticker.C:
				w.flushLogBuffer()
			}
		}
	}()

	// reasign global functions
	zlog = NewDefaultLogger(w)
	sugar = zlog.Sugar()
	Printf = sugar.Infof
	Println = sugar.Info

	Debugf = sugar.Debugf
	Infof = sugar.Infof
	Info = sugar.Info
	Warnf = sugar.Warnf
	Warn = sugar.Warn
	Error = sugar.Error
	Errorf = sugar.Errorf
	Fatalf = sugar.Fatalf
	Fatal = sugar.Fatal

	With = sugar.With
	IsDebugLevel = zlog.Level() == zapcore.DebugLevel
	return nil
}

func (w *S3LogWriter) blobKey() *string {
	now := time.Now().UTC()
	v := fmt.Sprintf("%s/%s/%s/%s/%v.log",
		w.appName, w.environment, w.keySuffix, now.Format("2006-01-02"), now.UnixMilli())
	return aws.String(v)
}

// newS3Client creates a new s3 client with static credentials.
// It's important that the credentials have only permission to update objects in the target bucket
// since the credentials are embedded in the binary
func newS3Client() (*s3.Client, error) {
	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()
	config, err := config.LoadDefaultConfig(
		timeoutCtx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"AKIAS5FK7DQJFWVJ67KQ",
			"qAhcobhRU6wSyieDJGm1l+nYIfUOVG74FNn3IqTc",
			"",
		)),
		config.WithRegion("us-east-1"),
	)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(config), nil
}
