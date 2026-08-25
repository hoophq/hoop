// Package conformance exercises the Gate: policy decisions, auditing, and response masking over
// real wire frames
// against the real protocol codecs.
//
// It lives outside the root module on purpose. The codecs ship in
// github.com/hoophq/libhoop/v2, a private module, and a test-only
// dependency still lands in go.mod — importing them from the root would make
// github.com/hoophq/hoop/hoopinspect unbuildable for anyone without access to
// it. Keeping the root at zero dependencies is the product requirement stated
// at the top of its go.mod, and it applies to test dependencies too.
//
// There is no non-test code here; the package exists to hold the files.
package conformance
