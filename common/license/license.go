package license

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// openssl rsa -in ./license.key -pubout
var licensePubKeyPem = []byte(`
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAuMaf59LDDC5t06jYtXJB
xDM3+e1POErhDzV1KcATYN0PS39yeqZ4VYxOr/0b8iqoPmYfReoj1GBiXKkMrO5D
BOCCFwSUGnEAPVBUsGhcbtPmEW8iJvMCdiG35GpWgBbn8Q5TAMdEweGQSBo0CPRz
xaOLeCgMv5qx10KpnP/8SRaDmM0vvOksRwJAMmwMaSkQEKOrs97jkDgnBY1mz1TI
zmo40K3nFT6WHgqETIrl3t/fC1Fv25MDrPLE4M3htqBKLKDR99pPHX0gxB3dvwi6
p8mG+hifq6xb6bTDH7ilIhFf30v+jjSfLyZUl56xitSiqF92uJTOZ5Q9xqISo7Sq
yQIDAQAB
-----END PUBLIC KEY-----
`)

const (
	OSSType        string = "oss"
	EnterpriseType string = "enterprise"
)

var (
	allowedLicenseTypes = []string{OSSType, EnterpriseType}

	ErrNotValid               = errors.New("license is not valid, contact your administrator or our support at https://help.hoop.dev")
	ErrDataMaskingUnsupported = errors.New("data masking is enabled for this connection but is not supported with the open source license, disable it or contact our support at https://help.hoop.dev")
	ErrWebhooksUnsupported    = errors.New("webhooks is enabled for this connection but is not supported with the open source license, disable it or contact our support at https://help.hoop.dev")
	ErrUsedBeforeIssued       = errors.New("license used before issued")
	ErrExpired                = errors.New("license expired")
)

type Payload struct {
	Type         string   `json:"type"`
	IssuedAt     int64    `json:"issued_at"`
	ExpireAt     int64    `json:"expire_at"`
	AllowedHosts []string `json:"allowed_hosts"`
	Description  string   `json:"description"`
}

type License struct {
	Payload   Payload `json:"payload"`
	KeyID     string  `json:"key_id"`
	Signature string  `json:"signature"`
}

func Sign(privKey *rsa.PrivateKey, licenseType, description string, allowedHosts []string, expireAt time.Duration) (*License, error) {
	clockTolerance := -time.Hour
	issuedAt := time.Now().UTC().Add(clockTolerance)
	expireAtTime := time.Now().UTC().Add(expireAt)
	return sign(privKey, licenseType, description, allowedHosts, issuedAt, expireAtTime)
}

func sign(privKey *rsa.PrivateKey, licenseType, description string, allowedHosts []string, issuedAt, expireAt time.Time) (*License, error) {
	if !slices.Contains(allowedLicenseTypes, licenseType) {
		return nil, fmt.Errorf("unknown license type: %q, allowed values: %v",
			licenseType, allowedLicenseTypes)
	}
	keyID, err := pubkeyChecksum(&privKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate public key id: %v", err)
	}
	payload := Payload{
		Type:         licenseType,
		IssuedAt:     issuedAt.Unix(),
		ExpireAt:     expireAt.Unix(),
		AllowedHosts: allowedHosts,
		Description:  description,
	}

	msgHash := sha256.New()
	if _, err = msgHash.Write(payload.signingData()); err != nil {
		return nil, err
	}
	msgHashSum := msgHash.Sum(nil)
	signature, err := rsa.SignPSS(rand.Reader, privKey, crypto.SHA256, msgHashSum, nil)
	if err != nil {
		return nil, err
	}
	return &License{
		Payload:   payload,
		KeyID:     keyID,
		Signature: base64.StdEncoding.EncodeToString(signature),
	}, nil
}

// Parse the license data in json, verify if the license is valid
func Parse(licenseData []byte, hostAddr string) (*License, error) {
	var l License
	if err := json.Unmarshal(licenseData, &l); err != nil {
		return nil, fmt.Errorf("failed decoding license data, reason=%v", err)
	}
	if err := l.Verify(); err != nil {
		return &l, err
	}
	return &l, l.VerifyHost(hostAddr)
}

// Verify validate the attributes and if the license is valid
func (l License) Verify() error { return l.verify(time.Now().UTC()) }
func (l License) verify(now time.Time) error {
	if err := l.validateAttributes(); err != nil {
		return err
	}
	pubkey, err := parsePublicKey()
	if err != nil {
		return err
	}
	msgHash := sha256.New()
	signature, err := base64.StdEncoding.DecodeString(string(l.Signature))
	if err != nil {
		return fmt.Errorf("failed decoding license payload signature: %v", err)
	}
	_, err = msgHash.Write(l.Payload.signingData())
	if err != nil {
		return fmt.Errorf("failed generating payload sha256 hash: %v", err)
	}
	msgHashSum := msgHash.Sum(nil)
	err = rsa.VerifyPSS(pubkey, crypto.SHA256, msgHashSum, []byte(signature), nil)
	if err != nil {
		return fmt.Errorf("failed verifying license signature: %v", err)
	}
	issuedAt, expireAt := time.Unix(l.Payload.IssuedAt, 0).In(time.UTC), time.Unix(l.Payload.ExpireAt, 0).In(time.UTC)
	if issuedAt.IsZero() || expireAt.IsZero() {
		return fmt.Errorf("unable to parse timestamp for issued_at or expire_at attributes")
	}
	if now.Before(issuedAt) {
		return ErrUsedBeforeIssued
	}
	if now.After(expireAt) {
		return ErrExpired
	}
	return nil
}

func (l License) validateAttributes() error {
	if !slices.Contains(allowedLicenseTypes, l.Payload.Type) {
		return fmt.Errorf("unknown license type: %q, allowed values: %v",
			l.Payload.Type, allowedLicenseTypes)
	}
	if len(l.Payload.AllowedHosts) == 0 {
		return fmt.Errorf("missing allowed hosts")
	}
	if len(l.Payload.Description) == 0 {
		return fmt.Errorf("missing description")
	}
	if l.Payload.IssuedAt == 0 || l.Payload.ExpireAt == 0 {
		return fmt.Errorf("missing or invalid issued_at, expire_at attributes")
	}
	if l.Signature == "" {
		return fmt.Errorf("signature attribute is empty")
	}
	return nil
}

// signingData is the payload to be signed and verified
func (p Payload) signingData() []byte {
	v := p.Type + ":" +
		fmt.Sprintf("%v", p.IssuedAt) + ":" +
		fmt.Sprintf("%v", p.ExpireAt) + ":" +
		strings.Join(p.AllowedHosts, ",") + ":" +
		p.Description
	return []byte(v)
}

func (l License) VerifyHost(host string) error {
	if host == "localhost" || host == "127.0.0.1" {
		return nil
	}
	for _, allowedHost := range l.Payload.AllowedHosts {
		var isWildcard bool
		if len(allowedHost) > 0 {
			isWildcard = allowedHost[0] == '*'
		}
		allowedHost = strings.TrimPrefix(allowedHost, "*.")
		if host == allowedHost || allowedHost == "*" {
			return nil
		}
		if isWildcard {
			if strings.HasSuffix(host, allowedHost) {
				return nil
			}
		}
	}
	return fmt.Errorf("host %q is not allowed to use this license, allowed hosts: %v", host, l.Payload.AllowedHosts)
}

func parsePublicKey() (*rsa.PublicKey, error) {
	block, _ := pem.Decode(licensePubKeyPem)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("failed to decode PEM block containing public key")
	}
	obj, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unable to parse public key: %v", err)
	}
	rsaPub, ok := obj.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("failed to coerce to *rsa.PublicKey, got=%T", obj)
	}
	return rsaPub, nil
}

// pubkeyChecksum creates a sha256 of the public key
// to be userd as a key identifier
func pubkeyChecksum(pubkey *rsa.PublicKey) (string, error) {
	encBytes, err := x509.MarshalPKIXPublicKey(pubkey)
	if err != nil {
		return "", err
	}
	msgHash := sha256.New()
	_, err = msgHash.Write(encBytes)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(msgHash.Sum(nil)), nil
}
