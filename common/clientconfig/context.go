package clientconfig

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Profile is one account inside $HOME/.hoop/config.toml.
type Profile struct {
	Name          string
	Token         string
	APIURL        string
	GrpcURL       string
	TLSCA         string
	SkipTLSVerify bool
	Current       bool
}

// ProfileFile is config.toml when it holds [profile <name>] sections.
// Current selects the account. An empty Current uses the first profile.
type ProfileFile struct {
	Current  string
	Profiles []Profile
	flat     Profile
	hasFlat  bool
}

// ParseProfileFile reads a flat config or a file of [profile <name>] sections.
// [profile profile1] and [profile.profile1] are the same section.
func ParseProfileFile(data []byte) (ProfileFile, error) {
	var doc ProfileFile
	var current *Profile
	lineNo := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		lineNo++
		text := strings.TrimSpace(strings.TrimSuffix(string(line), "\r"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.HasPrefix(text, "[") {
			name, err := profileHeader(text)
			if err != nil {
				return ProfileFile{}, fmt.Errorf("line %d: %w", lineNo, err)
			}
			if doc.hasFlat {
				return ProfileFile{}, fmt.Errorf("line %d: profile %q is mixed with top-level keys", lineNo, name)
			}
			for _, existing := range doc.Profiles {
				if existing.Name == name {
					return ProfileFile{}, fmt.Errorf("duplicate profile %q", name)
				}
			}
			doc.Profiles = append(doc.Profiles, Profile{Name: name})
			current = &doc.Profiles[len(doc.Profiles)-1]
			continue
		}
		key, raw, ok := strings.Cut(text, "=")
		if !ok {
			return ProfileFile{}, fmt.Errorf("line %d: expected key = value", lineNo)
		}
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)
		if current == nil && key == "current" {
			name, err := parseString(raw)
			if err != nil {
				return ProfileFile{}, fmt.Errorf("line %d: current: %w", lineNo, err)
			}
			if !validProfileName(name) {
				return ProfileFile{}, fmt.Errorf("line %d: invalid profile name %q", lineNo, name)
			}
			doc.Current = name
			continue
		}
		if current == nil {
			if err := doc.setFlat(key, raw); err != nil {
				return ProfileFile{}, fmt.Errorf("line %d: %w", lineNo, err)
			}
			continue
		}
		if err := setProfileField(current, key, raw); err != nil {
			return ProfileFile{}, fmt.Errorf("line %d: profile %q: %w", lineNo, current.Name, err)
		}
	}
	if doc.Current != "" && doc.HasProfiles() {
		if _, err := doc.Active(); err != nil {
			return ProfileFile{}, err
		}
	}
	return doc, nil
}

// HasProfiles reports whether the file selects among named profiles.
func (f ProfileFile) HasProfiles() bool { return len(f.Profiles) > 0 }

// Active returns the selected profile.
func (f ProfileFile) Active() (Profile, error) {
	idx, err := f.activeIndex()
	if err != nil {
		return Profile{}, err
	}
	p := f.Profiles[idx]
	p.Current = true
	return p, nil
}

// Use selects name. The other profiles stay in the file.
func (f *ProfileFile) Use(name string) (Profile, error) {
	if !f.HasProfiles() {
		return Profile{}, fmt.Errorf("config.toml has no [profile <name>] sections")
	}
	for i := range f.Profiles {
		if f.Profiles[i].Name == name {
			f.Current = name
			p := f.Profiles[i]
			p.Current = true
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("profile %q not found. Run `hoop config get-contexts`", name)
}

// ApplyActive writes credential fields onto the selected profile.
func (f *ProfileFile) ApplyActive(fields Profile) error {
	idx, err := f.activeIndex()
	if err != nil {
		return err
	}
	name := f.Profiles[idx].Name
	f.Profiles[idx].Token = fields.Token
	f.Profiles[idx].APIURL = fields.APIURL
	f.Profiles[idx].GrpcURL = fields.GrpcURL
	f.Profiles[idx].TLSCA = fields.TLSCA
	f.Profiles[idx].SkipTLSVerify = fields.SkipTLSVerify
	if f.Current == "" {
		f.Current = name
	}
	return nil
}

// Upsert stores a profile and selects it.
func (f *ProfileFile) Upsert(p Profile) error {
	if !validProfileName(p.Name) {
		return fmt.Errorf("invalid profile name %q", p.Name)
	}
	if p.APIURL == "" {
		return fmt.Errorf("profile %q has no api_url", p.Name)
	}
	for i := range f.Profiles {
		if f.Profiles[i].Name == p.Name {
			f.Profiles[i] = p
			f.Current = p.Name
			return nil
		}
	}
	f.Profiles = append(f.Profiles, p)
	f.Current = p.Name
	f.hasFlat = false
	return nil
}

// Delete removes a profile. The selected profile stays.
func (f *ProfileFile) Delete(name string) error {
	if !f.HasProfiles() {
		return fmt.Errorf("profile %q not found. Run `hoop config get-contexts`", name)
	}
	active, err := f.Active()
	if err != nil {
		return err
	}
	if active.Name == name {
		return fmt.Errorf("profile %q is active. Switch to another profile first", name)
	}
	next := f.Profiles[:0]
	found := false
	for _, p := range f.Profiles {
		if p.Name == name {
			found = true
			continue
		}
		next = append(next, p)
	}
	if !found {
		return fmt.Errorf("profile %q not found. Run `hoop config get-contexts`", name)
	}
	f.Profiles = next
	return nil
}

// Marshal writes current and each [profile <name>] section.
func (f ProfileFile) Marshal() []byte {
	var buf bytes.Buffer
	if f.Current != "" {
		fmt.Fprintf(&buf, "current = %s\n\n", strconv.Quote(f.Current))
	}
	for i, p := range f.Profiles {
		if i > 0 {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "[profile %s]\n", p.Name)
		fmt.Fprintf(&buf, "token = %s\n", strconv.Quote(p.Token))
		fmt.Fprintf(&buf, "api_url = %s\n", strconv.Quote(p.APIURL))
		fmt.Fprintf(&buf, "grpc_url = %s\n", strconv.Quote(p.GrpcURL))
		if p.TLSCA != "" {
			fmt.Fprintf(&buf, "tls_ca = %s\n", strconv.Quote(p.TLSCA))
		}
		fmt.Fprintf(&buf, "skip_tls_verify = %t\n", p.SkipTLSVerify)
	}
	return buf.Bytes()
}

// Flat returns top-level credentials when the file has no profile sections.
func (f ProfileFile) Flat() (Profile, bool) {
	if f.HasProfiles() || !f.hasFlat {
		return Profile{}, false
	}
	return f.flat, true
}

// MarshalFlat writes one account for a CLI that reads a flat config.toml.
func MarshalFlat(p Profile) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "token = %s\n", strconv.Quote(p.Token))
	fmt.Fprintf(&buf, "api_url = %s\n", strconv.Quote(p.APIURL))
	fmt.Fprintf(&buf, "grpc_url = %s\n", strconv.Quote(p.GrpcURL))
	if p.TLSCA != "" {
		fmt.Fprintf(&buf, "tls_ca = %s\n", strconv.Quote(p.TLSCA))
	}
	fmt.Fprintf(&buf, "skip_tls_verify = %t\n", p.SkipTLSVerify)
	return buf.Bytes()
}

// ListProfiles reads named profiles from one config file.
func ListProfiles(path string) ([]Profile, error) {
	doc, err := readProfilePath(path)
	if err != nil {
		return nil, err
	}
	if !doc.HasProfiles() {
		return nil, nil
	}
	out := make([]Profile, len(doc.Profiles))
	copy(out, doc.Profiles)
	if idx, err := doc.activeIndex(); err == nil {
		out[idx].Current = true
	}
	return out, nil
}

// CurrentProfile returns the selected profile name.
func CurrentProfile(path string) (string, error) {
	doc, err := readProfilePath(path)
	if err != nil {
		return "", err
	}
	active, err := doc.Active()
	if err != nil {
		return "", err
	}
	return active.Name, nil
}

// UseProfile selects a profile inside the same config file.
func UseProfile(path, name string) (Profile, error) {
	doc, err := readProfilePath(path)
	if err != nil {
		return Profile{}, err
	}
	selected, err := doc.Use(name)
	if err != nil {
		return Profile{}, err
	}
	if err := writeProfilePath(path, doc); err != nil {
		return Profile{}, err
	}
	return selected, nil
}

// SaveProfile stores the active credentials under name in the same file.
func SaveProfile(path, name string) (Profile, error) {
	if !validProfileName(name) {
		return Profile{}, fmt.Errorf("invalid profile name %q", name)
	}
	doc, err := readProfilePath(path)
	if err != nil {
		return Profile{}, err
	}
	var next Profile
	switch {
	case doc.HasProfiles():
		next, err = doc.Active()
		if err != nil {
			return Profile{}, err
		}
	case doc.hasFlat && doc.flat.APIURL != "":
		next = doc.flat
	default:
		return Profile{}, fmt.Errorf("active config has no api_url")
	}
	next.Name = name
	next.Current = false
	if err := doc.Upsert(next); err != nil {
		return Profile{}, err
	}
	if err := writeProfilePath(path, doc); err != nil {
		return Profile{}, err
	}
	next.Current = true
	return next, nil
}

// DeleteProfile removes a profile from the config file.
func DeleteProfile(path, name string) error {
	doc, err := readProfilePath(path)
	if err != nil {
		return err
	}
	if err := doc.Delete(name); err != nil {
		return err
	}
	return writeProfilePath(path, doc)
}

// UpdateActiveProfile writes credential fields onto the selected profile.
func UpdateActiveProfile(path string, fields Profile) error {
	doc, err := readProfilePath(path)
	if err != nil {
		return err
	}
	if !doc.HasProfiles() {
		return errNotProfiles
	}
	if err := doc.ApplyActive(fields); err != nil {
		return err
	}
	return writeProfilePath(path, doc)
}

// errNotProfiles tells Save to keep the flat-file encoder.
var errNotProfiles = fmt.Errorf("config file has no profiles")

// IsNotProfiles reports that path is a flat config file.
func IsNotProfiles(err error) bool { return err == errNotProfiles }

func readProfilePath(path string) (ProfileFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProfileFile{}, err
	}
	doc, err := ParseProfileFile(data)
	if err != nil {
		return ProfileFile{}, fmt.Errorf("failed decoding %s: %w", path, err)
	}
	return doc, nil
}

func writeProfilePath(path string, doc ProfileFile) error {
	return writePrivate(path, doc.Marshal())
}

func (f ProfileFile) activeIndex() (int, error) {
	if !f.HasProfiles() {
		return 0, fmt.Errorf("current profile is not set")
	}
	if f.Current == "" {
		return 0, nil
	}
	for i := range f.Profiles {
		if f.Profiles[i].Name == f.Current {
			return i, nil
		}
	}
	return 0, fmt.Errorf("profile %q is selected but it is not in the config file", f.Current)
}

func (f *ProfileFile) setFlat(key, raw string) error {
	if len(f.Profiles) > 0 {
		return fmt.Errorf("top-level %q is mixed with profiles", key)
	}
	f.hasFlat = true
	return setProfileField(&f.flat, key, raw)
}

func setProfileField(p *Profile, key, raw string) error {
	switch key {
	case "token":
		value, err := parseString(raw)
		if err != nil {
			return err
		}
		p.Token = value
	case "api_url":
		value, err := parseString(raw)
		if err != nil {
			return err
		}
		p.APIURL = value
	case "grpc_url":
		value, err := parseString(raw)
		if err != nil {
			return err
		}
		p.GrpcURL = value
	case "tls_ca":
		value, err := parseString(raw)
		if err != nil {
			return err
		}
		p.TLSCA = value
	case "skip_tls_verify":
		value, err := parseBool(raw)
		if err != nil {
			return err
		}
		p.SkipTLSVerify = value
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

func profileHeader(text string) (string, error) {
	if !strings.HasPrefix(text, "[") || !strings.HasSuffix(text, "]") {
		return "", fmt.Errorf("invalid profile header %q", text)
	}
	body := strings.TrimSpace(text[1 : len(text)-1])
	const dotted = "profile."
	const spaced = "profile "
	var name string
	switch {
	case strings.HasPrefix(body, dotted):
		name = body[len(dotted):]
	case strings.HasPrefix(body, spaced):
		name = strings.TrimSpace(body[len(spaced):])
	default:
		return "", fmt.Errorf("invalid profile header %q", text)
	}
	if !validProfileName(name) {
		return "", fmt.Errorf("invalid profile name %q", name)
	}
	return name, nil
}

func validProfileName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '_' || r == '.' || r == '-'):
		default:
			return false
		}
	}
	return true
}

func parseBool(raw string) (bool, error) {
	text, err := parseString(raw)
	if err != nil {
		return false, err
	}
	switch text {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool %q", text)
	}
}

func parseString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty value")
	}
	switch raw[0] {
	case '"':
		value, rest, err := unquotePrefix(raw)
		if err != nil {
			return "", err
		}
		rest = strings.TrimSpace(rest)
		if rest != "" && !strings.HasPrefix(rest, "#") {
			return "", fmt.Errorf("trailing %q", rest)
		}
		return value, nil
	case '\'':
		end := strings.IndexByte(raw[1:], '\'')
		if end < 0 {
			return "", fmt.Errorf("unclosed string")
		}
		rest := strings.TrimSpace(raw[2+end:])
		if rest != "" && !strings.HasPrefix(rest, "#") {
			return "", fmt.Errorf("trailing %q", rest)
		}
		return raw[1 : 1+end], nil
	default:
		if i := strings.Index(raw, " #"); i >= 0 {
			raw = strings.TrimSpace(raw[:i])
		}
		return raw, nil
	}
}

func unquotePrefix(raw string) (string, string, error) {
	for i := 1; i < len(raw); i++ {
		if raw[i] == '\\' {
			i++
			if i >= len(raw) {
				return "", "", fmt.Errorf("unclosed string")
			}
			continue
		}
		if raw[i] == '"' {
			value, err := strconv.Unquote(raw[:i+1])
			if err != nil {
				return "", "", err
			}
			return value, raw[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("unclosed string")
}

func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".ctx-*")
	if err != nil {
		return fmt.Errorf("failed saving %s: %w", path, err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed saving %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed saving %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed saving %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed saving %s: %w", path, err)
	}
	success = true
	return nil
}
