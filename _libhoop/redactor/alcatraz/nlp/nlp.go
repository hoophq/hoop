// Package nlp is the open-source stub for the enterprise adapter that binds
// an in-process ONNX NER model to the alcatraz redactor client. The model
// runtime ships only in the private libhoop module; keeping the stub free of
// it is the point, since importing that package is what links the runtime.
package nlp

import (
	"errors"

	redactoralcatraz "libhoop/redactor/alcatraz"
)

// Provider returns a provider that always fails: this build has no model
// runtime to load one with. It fails rather than returning a nil backend so
// that a caller reaching it — none does today, since the OSS SetNlpProvider
// discards what it is given — is refused instead of silently running with
// the statistical entity types (PERSON, LOCATION, NRP) undetected.
func Provider(modelsDir string) func() (redactoralcatraz.NlpBackend, error) {
	return func() (redactoralcatraz.NlpBackend, error) {
		return nil, errors.New("missing redaction hoop library for the alcatraz NER backend, contact your administrator")
	}
}
