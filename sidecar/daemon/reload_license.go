package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
)

// A license changes at runtime in three ways, and a restart reads the same
// answer as the running process in every one of them:
//
//   - the owner's document names another license: a plane rotation, or an
//     edit of the config file's `license` key (applyOwned);
//   - the file a local source names is replaced behind the same path: a
//     renewal dropped onto a mount (pollLicense);
//   - an operator installs one through PUT /license on the admin listener
//     (installLicense). That one lives in memory: the next change to the
//     source in force replaces it, and so does a restart.
//
// None of them drops a rule to fit a smaller license. A rotation the owner
// made is published and the relay stops, the controlled drain an ended term
// gets; an install through the admin API is refused instead, because a
// caller holding the admin secret must not be able to stop the relay.

var (
	// errLicenseManaged refuses an admin install under a plane that owns
	// the license: the fleet's license is set in one place.
	errLicenseManaged = errors.New("the control plane owns the license of this sidecar; " +
		"set the organization's license in the control plane")
	// errLicenseRefused wraps every reason an admin install was refused.
	errLicenseRefused = errors.New("license refused")
)

// claimLicense runs, under mu, when the plane's license header first says it
// manages the license: a gateway upgraded under a running sidecar. From then
// on the plane is the only source, as resolveLicenseFor decides at startup:
// no local file or PUT /license changes it again, for the life of the
// process. The plane's license itself is evaluated by applyOwned together
// with the document that carries it, so an answer that narrows the license
// and trims the rules applies as one generation.
func (r *reloader) claimLicense(log *slog.Logger) {
	r.licManaged = true
	r.licPinned = license.Ref{}
	r.seedLicenseFile()
	r.licClaimed = true
	// The document may be byte-identical to the last one: its license key
	// now means something else, so it runs again.
	r.lastHandled = nil
	log.Info("the control plane now manages the license; local license sources and PUT /license no longer change it")
}

// licenseFile is the reference pollLicense watches: the source in force when
// it names a path. Empty under a plane that owns the license, and for a
// document given inline, which cannot change in a running process.
func (r *reloader) licenseFile() license.Ref {
	var ref license.Ref
	switch {
	case r.licManaged:
	case r.licPinned.Value != "":
		ref = r.licPinned
	case r.licOver == "" && !r.planeOwned:
		// The config key, which a file edit can point somewhere else. An
		// embedder's UseLicense outranks it and names no path.
		ref = license.Ref{Value: r.licRaw, Source: fileLicenseSource}
	}
	if !licenseIsPath(ref.Value) {
		return license.Ref{}
	}
	return ref
}

// seedLicenseFile starts watching the license file in force. The baseline is
// the license in force, not a fresh read: a file replaced between the load
// that put it in force and this seed would otherwise become the baseline and
// never be adopted. The first poll reads the file and compares.
func (r *reloader) seedLicenseFile() {
	r.licFile = strings.TrimSpace(r.licenseFile().Value)
	r.licFileSize, r.licFileMod = 0, time.Time{}
	r.licFileDoc = r.lic.get()
	r.licFilePending = r.licFile != ""
	r.licFileBroken = false
}

// pollLicense is one look at the license file in force: re-read it when its
// size or mtime moved, when the last read failed, or when forced (SIGHUP).
// A document different from the one the file held last time is a rotation.
//
// The compare is against the FILE's last document, not the license in
// force, so re-reading an unchanged file never undoes an admin install.
//
// Polling for the reason the config file is polled: a Kubernetes Secret or
// ConfigMap volume update is an atomic symlink swap that a stat follows.
func (r *reloader) pollLicense(log *slog.Logger, forced bool) {
	ref := r.licenseFile()
	if strings.TrimSpace(ref.Value) != r.licFile {
		// The config key now names another file, or none.
		r.seedLicenseFile()
	}
	if r.licFile == "" {
		return
	}
	st, err := os.Stat(r.licFile)
	if err != nil {
		r.licFilePending = true
		if !r.licFileBroken {
			log.Warn("the license file cannot be read; keeping the license in use",
				"path", r.licFile, "source", ref.Source, "error", err)
			r.licFileBroken = true
		}
		return
	}
	if !forced && !r.licFilePending && st.Size() == r.licFileSize && st.ModTime().Equal(r.licFileMod) {
		return
	}
	r.licFileSize, r.licFileMod = st.Size(), st.ModTime()
	next := license.Load(ref)
	if next.State() == license.StateInvalid {
		// Retried every tick: the read may have caught a writer mid-file.
		r.licFilePending = true
		if !r.licFileBroken {
			log.Warn("the license file does not load; keeping the license in use",
				"path", r.licFile, "error", next.Err)
			r.licFileBroken = true
		}
		return
	}
	r.licFilePending, r.licFileBroken = false, false
	if sameLicenseDoc(next, r.licFileDoc) {
		return
	}
	r.licFileDoc = next
	r.rotateLicense(log, next)
}

// rotateLicense adopts a document the file of the source in force now holds.
// The owner changed it, so it is the license whether or not it covers the
// running rules, exactly as a plane rotation is: one that does not is
// published and the relay stops, and the restart refuses the config by name.
func (r *reloader) rotateLicense(log *slog.Logger, next license.Status) {
	if sameLicenseDoc(next, r.lic.get()) {
		return
	}
	problems := r.licenseProblems(next)
	r.lic.set(next)
	if len(problems) > 0 {
		r.lic.overCap.Store(true)
		log.Warn("the license file no longer covers the running rules; "+
			"stopping the relay so it restarts under the new license",
			"path", r.licFile, "license", next.Line(), "error", strings.Join(problems, "; "))
		return
	}
	// The new license covers the rules, so a stop a narrower one asked
	// for is no longer due.
	r.lic.overCap.Store(false)
	r.retryRefused()
	log.Info("license file replaced; the new license is in force", "path", r.licFile)
	log.Info(next.Line())
}

// retryRefused re-runs, on the next tick or heartbeat, a document refused
// under the license that was in force: the one now in force may cover it,
// and the dedupe would otherwise wait for another edit.
func (r *reloader) retryRefused() {
	if r.lastOutcome == reloadRefused {
		r.lastHandled = nil
		r.filePending = true
	}
}

// licenseProblems is what the running rules would hit under next: the caps
// buildLanes enforces, and the hosted-model default that an Enterprise
// license turns into a refusal. Empty means next covers them.
func (r *reloader) licenseProblems(next license.Status) []string {
	c := *r.running
	c.lic = next
	problems := c.checkLimits(next)
	if r.ac != nil && r.ac.cfg != nil && r.ac.cfg.hostedDefault {
		if _, err := c.effectiveAnalyzer(); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return problems
}

// installLicense adopts a license document sent to the admin API. installed
// is false when the document is the one already in force.
//
// Stricter than a rotation, because the caller is not the owner of the
// config: only a current license is taken, and one that does not cover the
// running rules is refused rather than stopping the relay.
func (r *reloader) installLicense(log *slog.Logger, doc string) (lic license.Status, installed bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.licManaged {
		return license.Status{}, false, errLicenseManaged
	}
	doc = strings.TrimSpace(doc)
	if doc == "" || doc[0] != '{' {
		// license.Load reads anything else as a PATH. A request must never
		// make this process open a file it names.
		return license.Status{}, false, fmt.Errorf("%w: the body must be the license document Hoop issued, "+
			"braces included", errLicenseRefused)
	}
	next := license.Load(license.Ref{Value: doc, Source: adminLicenseSource})
	switch next.State() {
	case license.StateValid:
	case license.StateExpired:
		return next, false, fmt.Errorf("%w: %s", errLicenseRefused, next.Reason())
	default:
		return next, false, fmt.Errorf("%w: %v", errLicenseRefused, next.Err)
	}
	if cur := r.lic.get(); sameLicenseDoc(next, cur) {
		return cur, false, nil
	}
	if problems := r.licenseProblems(next); len(problems) > 0 {
		return next, false, fmt.Errorf("%w: it does not cover the running configuration: %s",
			errLicenseRefused, strings.Join(problems, "; "))
	}
	r.lic.set(next)
	r.lic.overCap.Store(false)
	r.retryRefused()
	log.Info("license installed through the admin API; it lasts until the license source " +
		"in force changes or the process restarts")
	log.Info(next.Line())
	return next, true, nil
}
