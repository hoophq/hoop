package alcatraz

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/hoophq/alcatraz/analyzer"
)

// Entity names for the credential recognizers this package adds.
//
// Alcatraz is a PII engine: it detects who someone is, across 51 identifier
// formats and 12 countries. It has no recognizer for a credential, and that
// gap is correct, because an AWS key is not personal data. But a response
// body carrying one is a worse leak than the same body carrying a phone
// number, and the relay masking that body should not have to care which
// engine a value came from.
//
// So these three are registered into the same alcatraz engine as everything
// else. They use the SCREAMING_SNAKE_CASE convention of the entities package
// so a config names them exactly like a built-in alcatraz type.
const (
	AWSAccessKey = "AWS_ACCESS_KEY"
	JWT          = "JWT"
	PrivateKey   = "PRIVATE_KEY"
)

// secretEntities is the set added on top of alcatraz's own.
var secretEntities = []string{AWSAccessKey, JWT, PrivateKey}

// registerSecrets adds the credential recognizers to a registry.
//
// Each is scored 1.0 or validated up to it, because these formats are
// unambiguous where a nine-digit number that looks like an SSN is not. "AKIA"
// followed by exactly 16 uppercase alphanumerics is an AWS access key id, and
// nothing else looks like that.
func registerSecrets(reg *analyzer.Registry, lang string) {
	reg.Add(lang, awsAccessKeyRecognizer())
	reg.Add(lang, jwtRecognizer())
	reg.Add(lang, privateKeyRecognizer())
}

func awsAccessKeyRecognizer() analyzer.Recognizer {
	return analyzer.NewPatternRecognizer(
		"AwsAccessKeyRecognizer", AWSAccessKey, "en",
		[]*analyzer.Pattern{
			// AKIA is the access-key-id prefix; ASIA is the temporary
			// session-token form, which leaks just as usefully.
			analyzer.MustPattern("AWS access key id", `\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`, 1.0),
		},
	)
}

func jwtRecognizer() analyzer.Recognizer {
	return analyzer.NewPatternRecognizer(
		"JwtRecognizer", JWT, "en",
		[]*analyzer.Pattern{
			// Three base64url segments. A bare three-segment pattern also
			// matches every dotted hostname and file path, so the header
			// must start with "eyJ", base64url of `{"`, which every JWT
			// header begins with, and the validator decodes it. The
			// signature segment may be empty: alg=none tokens are exactly
			// the ones worth catching.
			analyzer.MustPattern("JWT",
				`\beyJ[A-Za-z0-9_\-]{4,}\.[A-Za-z0-9_\-]{4,}\.[A-Za-z0-9_\-]*`, 0.6),
		},
	).WithValidator(validJWT)
}

func privateKeyRecognizer() analyzer.Recognizer {
	return analyzer.NewPatternRecognizer(
		"PrivateKeyRecognizer", PrivateKey, "en",
		[]*analyzer.Pattern{
			// The header alone is worthless to mask; the key material is the
			// body, so both alternatives have to consume it.
			//
			// The first takes a complete PEM block. The second is the
			// fallback for a block a response-size limit cut short, and it
			// must still eat the base64 that follows the header. Matching
			// the header alone would emit a placeholder directly above the
			// key material it claimed to remove. It stops at the first
			// character that cannot appear in a PEM body rather than running
			// to the end of the payload, so a truncated key does not swallow
			// unrelated rows.
			analyzer.MustPattern("PEM private key",
				`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`, 1.0),
			analyzer.MustPattern("PEM private key (truncated)",
				`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[A-Za-z0-9+/=\r\n\t ]*`, 1.0),
		},
	)
}

// validJWT decodes the header segment and requires it to be a JSON object
// naming an algorithm. It checks structure, not signature: this runs on a
// response body with no key material, so it answers "is this a token" and
// not "is this token valid".
func validJWT(s string) bool {
	// The regex guarantees two dots, so Cut always splits; a dotless string
	// fails the decode below.
	header, _, _ := strings.Cut(s, ".")

	// JWT segments are unpadded base64url, but be tolerant of a producer that
	// padded anyway.
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(header, "="))
	if err != nil {
		return false
	}
	var claims map[string]json.RawMessage
	if json.Unmarshal(raw, &claims) != nil {
		return false
	}
	_, hasAlg := claims["alg"]
	return hasAlg
}
