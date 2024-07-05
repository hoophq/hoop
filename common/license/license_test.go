package license

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTime(hour, minute int) time.Time {
	return time.Date(2024, time.July, 4, hour, minute, 0, 0, time.UTC)
}

func loadTestPrivKey2() *rsa.PrivateKey {
	// openssl genrsa -out ./hooplicense.key 2048
	privKeyBytes := []byte(`
-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCax7E1qC0bpTtD
eD0bLflB27GVlaxYzUUyttnKcJIygtOuQ/8b3FfWknzJMd+MkxyhPCANggMNeCpZ
eMJmiI5stxUqw9UJYamdyB/SeHNXFZWz/WE1jNVDD2Jgcv6t5PvvoDdH/U0TSmOs
RJM5kR2LhUiEcB5e8kPLoPmLNrx+eg0yAvD1q0dY9i/FSQ2W4FDgr0Jh6Rf/5loB
girQn2Cn9eHrKeNGOVxb+nDjy85N3hHX/g200yTOSMi9yyRkNzF+hSWCnMeL0U6h
rK3RMxrQC8m4AlwjZvziob7vEwxWp61HxfDL8ew5APYwhZXY2Ce2MTj+tq7RAt5G
0dTqW5DZAgMBAAECggEAAYn5Ankxup/D1TXHuMKWIwAf1caLVEY1OQ39oCBKqdco
augI7DJeircB59+3su5/B0DhajT32g1O8X0MhMe4j87ptldEYd+fV77mxxlUv0HL
D2M2cVl9QNmRLzeRffHkCePITO5RMv8HOu4jHxxI5Itel1eEi8nhn++Rr59LlD/X
NoGrrRI28hqgXL0ci/cXFPTjSv3aa4k/7yQ0BRWxBlhu36j0OFQN3ig08ZSry+Hc
SdpywJT06EZkBQ23pJdelpG/S7svwQ/MdNiRmrBfXvRq3I2H101gCUi2Y/OEtzT4
4FcnxI8I90ed2DubQwZUZiORn7ItelPDCnE98C3l0QKBgQDKO5SXqfESZQ3ZYuR7
1Jcov1OAkNRqFH8KxS7lD6HTqU6OzsjxQn8xHmrMa2J9JV0kD4Oz9vGr7L4GEnXQ
z8Z2Vw8+lX+BeOZUGMEdlWYVaoXjoSeetjnP3+PQ7grQulG8kHFQxtTzpxFoxktt
NN1v80qIotkfy2VomSaxo5lckQKBgQDD7l+9uZNnp4QrnXrmSvLbAys9E3dr0IYc
pecSw84FMlTu/f/bXPJrMb7xaGf9gZ9CdaNQcs+NUTHbfZ5rkhQK+orTcFZ9hHvj
pI7d9/FWZPfBEtIjfUwUPzHBi8xN/JyfhBk2Aonuk2BnHuop9snipRrvTzl4O+Iq
HoF99fEzyQKBgDCv+2wwC8vj7FujxWJSojm7Jj1ToPAREyzioBGhm9I7dqBHBHWh
DsIiko+4YrPCZRQjcA/JqhE8I9uOYjLtcthWyWLF1zayhrFEbGnU6AjL5oQQ7lr1
gCGdw1kvlgb+dGMzWzSZSfeHB1f0NYCLM6yaJB2VJzTSYQ23oWsu+eMhAoGBAKWt
FsY+etemfgvHcVn0vGDX0CMoJ85CGHV3D+r9KWOZiNpCa6yZbt+XxAcsKurhRcMT
6FIpkznDE66vDVuWvV3/N47NKkWe1ofK6YfmlethG2LmwEyEMeXY/gDUbqDvX50/
PXY/NVVIx7bLHGT5qwL8a8c6LbVupbLJ8uOJKTmJAoGAGzl6lka2u0YXwb+yaXmp
Wjn2EWhG4c18hixXci12z7AkFleuWHrB5IFdu0hExWAQJxc4TCqLlRiAr6HWx3gH
3KbmUxtQE8i8ghYDZIJ6Re1SPrIs8aUTrhWCZAJf26I1L04lYjjOPCNKcF8eZoEd
nT/L3K2fMvs+kdzmBrfENI0=
-----END PRIVATE KEY-----
		`)
	block, _ := pem.Decode(privKeyBytes)
	obj, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
	privkey, ok := obj.(*rsa.PrivateKey)
	if !ok {
		return nil
	}
	return privkey
}

func loadTestPrivKey() *rsa.PrivateKey {
	// openssl rsa -in hooplicense.key -pubout
	licensePubKeyPem = []byte(`
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwvQ9gN1nu3D+pthRH7UZ
kbgX9156xR1ppZ1GZn4wOfWli3WNdqttxDN8Z6UnKGAWpVC0prLf8WgRrVFd01E5
Icpas8YCLJyuZs6Tuv2hdJ2D71KaxL4XKYjf9hbb90ePaqUjvNC4OzJe6LW2mWlp
HmuIWTM01Ja/Jqzqq5Flb3APiMd59t6FSh8wWi3RxGSgJCynyY+SCJv/1cM3v0W+
tvpjSvVqvRxV63u1Tmf5e5T6ga5Ufv/9Bf0D3vgZOKMtS9jhZ7Ja7GS2Iz64GLnp
BkWBfw0T9y877adu1wWxUaSeHA2TFeh32BLCvnkDnEwNiik0lDrm/5Z3HhjHQYXa
1wIDAQAB
-----END PUBLIC KEY-----
	`)
	// openssl genrsa -out ./hooplicense.key 2048
	privKeyBytes := []byte(`
-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQDC9D2A3We7cP6m
2FEftRmRuBf3XnrFHWmlnUZmfjA59aWLdY12q23EM3xnpScoYBalULSmst/xaBGt
UV3TUTkhylqzxgIsnK5mzpO6/aF0nYPvUprEvhcpiN/2Ftv3R49qpSO80Lg7Ml7o
tbaZaWkea4hZMzTUlr8mrOqrkWVvcA+Ix3n23oVKHzBaLdHEZKAkLKfJj5IIm//V
wze/Rb62+mNK9Wq9HFXre7VOZ/l7lPqBrlR+//0F/QPe+Bk4oy1L2OFnslrsZLYj
PrgYuekGRYF/DRP3Lzvtp27XBbFRpJ4cDZMV6HfYEsK+eQOcTA2KKTSUOub/lnce
GMdBhdrXAgMBAAECggEALQNv3/0/Ikxov+VaddO+36J+BiPOfQzZg9/YjXm9cOSD
ILw3uZrDcXXh15yOeggVsn37+DF8+6Rn0HjlDRHH+0FZyACEKADVU++GtLozOVXV
TMDp81tgxbpQ2+VTTLk9KAaRRdt7bk+nElxCmRF5sAhsJwxnul5ELI3ocUzU+vGh
rplbUwr+lYg7GQGAyhEdhidt8n6jSMxbMp+rmMqo5ZDDyZmQDd1Gc9ajBZ/5/07P
jV3rV2oUZkB4JPGGpJp6Z6/kTU88wvl5UECzY2mB+SvUNDOjZ0K1xJJSZArZen/v
HUOwCBp94Yh3nckdNUtGI7bq8LblxBgspXQFQEdLAQKBgQDmKeHz87S6nr/p4zqV
810hyFhHf2pNhqR5tsuOOhwR+Gu98msOHMP//qUFYd1j5OeGZ5cK8n5m7h2P+G7W
uyae5PL7TAJ1UbQdhfdCp+mt9a3gdLqn84g7p0qeeb7ZxgWR8LUfg7Os1ryx8hRp
oukFr9YoUsmuMdGP+/0YXVButwKBgQDY1o4x9UBdxxHZSkbpmnQ/gSr+Vj+yakm+
k3Laku31003cl9ubXgE8zOEgujYAdBV7vum7jVykGHeayZzBimSMvb+7hc2jHR87
ztkX2XxLiABcWoPUHHOwXgvC7OvvEndMGSmIjX6FXwVh1JGeq5/gaut3UVBcAoUT
kM6PWgPU4QKBgQDZuk0RJT2WPI53hojpSOqVBpzcJeA9rlzw9sbgqH/dUA88BJLZ
KsUO6ajZypZP5T5Pmrb7mCGS5TX5952CbFBAh3yD1IeOy9eDBjO9TnJ0KbBuYH4i
WvJI3BxuheTQxc6HHBl60m+p1Qlzm/lLZNzikFAanRZEPsRrXIkz/zITSQKBgDrN
FApgI3BKx4BRMCGxDM0bzfjikqtjP1Q6z+6N4ZHEF102oQrk1xkRxgsF9BbzY9AG
2YNOtkyZhfWnrqadTN8NpazIgBc3kny5fw2EoLwqyU5CDXW7sXOmTTIy5VgTfd5Z
BHZPSHwKZH8/Ea4hhF1rISdeGZiZ5lSD9D/TfS6BAoGBAMi5qNx7+qEsB8bNrfJv
M4XEjLF1SEysx03TmiiF7N+XUVtorVuZce+duc4GV1P9AWkH1vuClRU0ec/yaNQg
vDrkApCgwYBAAhNnaJS+BpxMxUWmbFYYNw5IlJ/94XYiAOD6wQBJixmIJw8cB+qy
k+MlQya0dU03Lx/AJnesT/wa
-----END PRIVATE KEY-----
		`)
	block, _ := pem.Decode(privKeyBytes)
	obj, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
	privkey, ok := obj.(*rsa.PrivateKey)
	if !ok {
		return nil
	}
	return privkey
}

func TestSignVerify(t *testing.T) {
	for _, tt := range []struct {
		msg string
		p   Payload
		now time.Time
		key *rsa.PrivateKey
		err error
	}{
		{
			msg: "it should be able to sign and verify",
			key: loadTestPrivKey(),
			p:   Payload{OSSType, newTime(10, 0).Unix(), newTime(10, 30).Unix(), []string{"*"}, "desc"},
			now: newTime(10, 15),
		},
		{
			msg: "it should return license expired error",
			key: loadTestPrivKey(),
			p:   Payload{OSSType, newTime(10, 0).Unix(), newTime(10, 30).Unix(), []string{"*"}, "desc"},
			now: newTime(10, 40),
			err: ErrExpired,
		},
		{
			msg: "it should return issued before used error",
			key: loadTestPrivKey(),
			p:   Payload{OSSType, newTime(10, 0).Unix(), newTime(10, 30).Unix(), []string{"*"}, "desc"},
			now: newTime(9, 59),
			err: ErrUsedBeforeIssued,
		},
		{
			msg: "it should return verification error when validating the signature",
			key: loadTestPrivKey2(),
			p:   Payload{EnterpriseType, newTime(10, 0).Unix(), newTime(10, 30).Unix(), []string{"*"}, "desc"},
			now: newTime(10, 15),
			err: fmt.Errorf("failed verifying license signature: crypto/rsa: verification error"),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			l, err := sign(tt.key, tt.p.Type, tt.p.Description, tt.p.AllowedHosts, time.Unix(tt.p.IssuedAt, 0), time.Unix(tt.p.ExpireAt, 0))
			if err != nil {
				t.Fatal(err)
			}
			err = l.verify(tt.now)
			if tt.err != nil {
				assert.EqualError(t, err, tt.err.Error())
			}
		})
	}
}

func TestVerifyHost(t *testing.T) {
	for _, tt := range []struct {
		msg   string
		p     Payload
		host  string
		isErr bool
	}{
		{
			msg:  "it should allow all matching host",
			p:    Payload{AllowedHosts: []string{"use.hoop.dev"}},
			host: "use.hoop.dev",
		},
		{
			msg:  "it should allow multiple hosts",
			p:    Payload{AllowedHosts: []string{"use.hoop.dev", "test.hoop.dev"}},
			host: "test.hoop.dev",
		},
		{
			msg:  "it should allow all hosts",
			p:    Payload{AllowedHosts: []string{"*"}},
			host: "anydomain.com",
		},
		{
			msg:  "it should allow wildcard hosts",
			p:    Payload{AllowedHosts: []string{"*.hoop.dev"}},
			host: "use.hoop.dev",
		},
		{
			msg:  "it should allow multiple wildcard hosts",
			p:    Payload{AllowedHosts: []string{"*.hoop.dev", "*.gateway.hoop.dev"}},
			host: "use.gateway.hoop.dev",
		},
		{
			msg:  "it should allow multiple wildcard hosts",
			p:    Payload{AllowedHosts: []string{"*.hoop.dev", "*.gateway.hoop.dev"}},
			host: "use.gateway.hoop.dev",
		},
		{
			msg:  "it should bypass verifying localhost",
			p:    Payload{AllowedHosts: []string{"use.hoop.dev"}},
			host: "localhost",
		},
		{
			msg:  "it should bypass verifying 127.0.0.1",
			p:    Payload{AllowedHosts: []string{"use.hoop.dev"}},
			host: "127.0.0.1",
		},
		{
			msg:   "it should NOT allow hosts that don't match",
			p:     Payload{AllowedHosts: []string{"use.hoop.dev"}},
			host:  "app.hoop.dev",
			isErr: true,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			err := (License{Payload: tt.p}).VerifyHost(tt.host)
			if tt.isErr {
				assert.Error(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}
