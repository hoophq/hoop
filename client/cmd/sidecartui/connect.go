package sidecartui

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/license"
)

// The Connect page: point this sidecar at a Control Plane, or start it
// with a license. A Control Plane is the Enterprise way to run sidecars,
// so the page says what it unlocks and where to ask for one, then takes
// the plane's URL and this sidecar's token. Connecting is checked here, the
// handshake included, so a wrong token or an unreachable plane is a line on
// this page, not an error after it closes.

// MeetURL is where "Talk to us" sends the person.
const MeetURL = "https://hoop.dev/meet"

// LicenseFile is the name the page saves a license under, in the folder
// FirstRunOptions.LicenseDir names.
const LicenseFile = "license.json"

type connectPage struct {
	form *form
	// busy is drawn while the plane is being reached.
	busy string
	// status is the last outcome, bad when it is a failure.
	status string
	bad    bool
}

func newConnectPage(licenseDir string) *connectPage {
	where := "a file only you can read"
	if licenseDir != "" {
		where = shortPath(filepath.Join(licenseDir, LicenseFile))
	}
	f := newForm("Connect to a Control Plane",
		&field{id: "connect", label: "Connect and boot", kind: fButton},
		&field{id: "meet", label: "Talk to us", kind: fButton},
		&field{id: "back", label: "Back", kind: fButton},
		&field{id: "about", kind: fNote, note: func() string {
			return "\n" + badge("ENTERPRISE", colPrimary) + "\n\n" + strings.Join([]string{
				stText.Render("A Control Plane runs your sidecars from the hoop web app: their"),
				stText.Render("listeners, their license, and the approval of what they hold."),
				stText.Render("With a license it unlocks ") + stStrong.Render("unlimited AI analyzer, guardrails and data"),
				stStrong.Render("masking") + stText.Render(", reviews in the web app, and much more."),
				"",
				stFaint.Render("No Control Plane with a license yet? Choose ") + stPrimary.Render("Talk to us") + stFaint.Render("."),
			}, "\n") + "\n"
		}},
		&field{id: "url", label: "Control Plane URL", kind: fText, placeholder: "https://hoop.example.com",
			help: "The address of your hoop Control Plane."},
		&field{id: "token", label: "Sidecar token", kind: fText, secret: true, placeholder: "hsc_…",
			help: "Shown once when the sidecar is registered in the Control Plane; a lost one means registering it again."},
		&field{id: "or", kind: fNote, note: func() string {
			return "\n" + stLabel.Render("OR START WITH A LICENSE") + "\n" + strings.Join([]string{
				stFaint.Render("Paste your license, or the path to its file. It is saved to " + where),
				stFaint.Render("and set as the license of the configs you set up from here."),
			}, "\n") + "\n"
		}},
		&field{id: "license", label: "License", kind: fText, placeholder: `{"payload": …} or /path/to/license.json`,
			help: "Checked before it is saved: an invalid or expired license is not written."},
		&field{id: "savelicense", label: "Save license", kind: fButton},
	)
	// The person came to type an address: start there, not on a button
	// that needs one.
	for i, x := range f.fields {
		if x.id == "url" {
			f.store()
			f.cur = i
			f.load()
		}
	}
	return &connectPage{form: f}
}

// connectInput reads and checks the URL and token before anything is sent.
func (p *connectPage) connectInput() (planeURL, token string, err error) {
	p.form.store()
	planeURL = strings.TrimRight(strings.TrimSpace(p.form.byID("url").text), "/")
	token = strings.TrimSpace(p.form.byID("token").text)
	if planeURL == "" || token == "" {
		return "", "", errors.New("enter the Control Plane URL and the sidecar token")
	}
	u, perr := url.Parse(planeURL)
	if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", fmt.Errorf("%q is not a web address; it starts with https:// and names a host", planeURL)
	}
	return planeURL, token, nil
}

// connectConflicts lists the ports the plane's listeners want that another
// program holds. The plane owns those addresses, so the answer is where to
// change them, not a rewrite here.
func connectConflicts(cfg *daemon.Config) []string {
	var out []string
	for _, u := range portUses(cfg, "") {
		if err := bindError(u.addr); err != nil {
			out = append(out, fmt.Sprintf("%s wants %s, which another program on this machine is using", u.what, u.addr))
		}
	}
	return out
}

// firstLine keeps an error to the sentence a person reads first. An
// unreachable plane keeps its plain half and says what to check; the
// request and socket detail after it is for logs, not for this page.
func firstLine(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if i := strings.Index(s, " is unreachable"); i > 0 {
		return s[:i+len(" is unreachable")] + ". Check the URL, and that this machine can reach it"
	}
	return s
}

// saveLicense checks a license the person pasted, or read from the file
// they named, and saves it in dir as LicenseFile, readable by them only.
// Nothing is written unless the license is valid now: a forged or expired
// one would only fail later, at the boot it was meant to unlock.
func saveLicense(input, dir string) (string, license.Status, error) {
	v := strings.TrimSpace(input)
	if v == "" {
		return "", license.Status{}, errors.New("paste your license, or the path to its file")
	}
	doc := v
	if !strings.HasPrefix(v, "{") {
		b, err := os.ReadFile(expand(v))
		if err != nil {
			return "", license.Status{}, fmt.Errorf("that is not a license, and no file is at %s", v)
		}
		doc = strings.TrimSpace(string(b))
	}
	st := license.Load(license.Ref{Value: doc, Source: "the license you entered"})
	switch st.State() {
	case license.StateValid:
	case license.StateExpired:
		return "", st, fmt.Errorf("this license expired on %s; talk to us to renew it", st.ExpiresAt().Format("2 Jan 2006"))
	case license.StateMissing:
		return "", st, errors.New("that is empty, not a license")
	default:
		return "", st, fmt.Errorf("this is not a valid hoop license: %s", st.Reason())
	}
	if dir == "" {
		return "", st, errors.New("there is no folder to save the license in")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", st, err
	}
	path := filepath.Join(dir, LicenseFile)
	if err := os.WriteFile(path, []byte(doc+"\n"), 0o600); err != nil {
		return "", st, err
	}
	// WriteFile keeps the mode of a file that was already there.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", st, err
	}
	return path, st, nil
}

// licenseLine describes a saved license in one line.
func licenseLine(st license.Status) string {
	who := "your organization"
	if st.License != nil && st.License.Payload.Description != "" {
		who = st.License.Payload.Description
	}
	return fmt.Sprintf("license for %s, valid until %s", who, st.ExpiresAt().Format("2 Jan 2006"))
}
