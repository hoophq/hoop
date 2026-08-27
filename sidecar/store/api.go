package store

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/session"
)

// API exposes a Store over HTTP as JSON.
//
// # Shipping the contract with the library
//
// The UI is a separate concern and a later conversation. The CONTRACT
// between it and the data outlives any particular frontend: field names,
// filter semantics, paging behavior. Pinning them here means a UI can be
// written, thrown away and rewritten without the storage layer moving.
//
// It also works without a UI at all. `curl` against these endpoints answers
// "what did alice run" during an incident, the question the whole library
// exists for.
//
// # Security boundary
//
// The handler provides no authentication, no authorization, and no CORS. It
// assumes you mounted it behind something that already decided the caller
// may see audit data. Exposing it directly hands out a read interface to
// every statement every user ran.
type API struct {
	store Store

	// BasePath is stripped from request paths before routing, so the handler
	// can be mounted under a prefix.
	BasePath string
}

// NewAPI returns a handler serving s.
func NewAPI(s Store) *API { return &API{store: s} }

// Routes returns a mux with the query endpoints registered:
//
//	GET /sessions            list sessions
//	GET /sessions/{id}       one session
//	GET /sessions/{id}/events   that session's timeline
//	GET /events              events across sessions
//	GET /stats               dashboard aggregates
//
// Every list endpoint accepts the filter query parameters documented on
// parseSessionFilter and parseEventFilter.
func (a *API) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions", a.listSessions)
	mux.HandleFunc("GET /sessions/{id}", a.getSession)
	mux.HandleFunc("GET /sessions/{id}/events", a.listSessionEvents)
	mux.HandleFunc("GET /events", a.listEvents)
	mux.HandleFunc("GET /stats", a.getStats)
	return mux
}

// ServeHTTP implements http.Handler.
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.BasePath != "" {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, a.BasePath)
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
	}
	a.Routes().ServeHTTP(w, r)
}

func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	f, err := parseSessionFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	page, err := a.store.Sessions(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	rec, err := a.store.Session(r.Context(), session.ID(r.PathValue("id")))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (a *API) listSessionEvents(w http.ResponseWriter, r *http.Request) {
	f, err := parseEventFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// The path segment wins over a query parameter: a caller cannot ask
	// /sessions/A/events?session_id=B and get B's timeline.
	f.SessionID = session.ID(r.PathValue("id"))

	page, err := a.store.Events(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	f, err := parseEventFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	page, err := a.store.Events(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) getStats(w http.ResponseWriter, r *http.Request) {
	f, err := parseSessionFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	stats, err := a.store.Stats(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// parseSessionFilter reads a SessionFilter from query parameters:
//
//	principal, connection, protocol   exact match
//	since, until                      RFC3339 timestamps
//	denied                            "true" for denials only
//	open                              "true" for live sessions only
//	q                                 substring search
//	limit, cursor                     paging
//
// An unparseable value is an error rather than a silent default. A filter
// that drops `since=yesterdy` shows the operator the wrong window with no
// signal that it happened.
func parseSessionFilter(r *http.Request) (SessionFilter, error) {
	q := r.URL.Query()
	f := SessionFilter{
		Principal:  q.Get("principal"),
		Connection: q.Get("connection"),
		Protocol:   inspect.Protocol(q.Get("protocol")),
		Search:     q.Get("q"),
		Cursor:     q.Get("cursor"),
	}

	var err error
	if f.Since, err = parseTime(q.Get("since")); err != nil {
		return f, errors.New("invalid 'since': " + err.Error())
	}
	if f.Until, err = parseTime(q.Get("until")); err != nil {
		return f, errors.New("invalid 'until': " + err.Error())
	}
	if f.DeniedOnly, err = parseBool(q.Get("denied")); err != nil {
		return f, errors.New("invalid 'denied': " + err.Error())
	}
	if f.OpenOnly, err = parseBool(q.Get("open")); err != nil {
		return f, errors.New("invalid 'open': " + err.Error())
	}
	if f.Limit, err = parseLimit(q.Get("limit")); err != nil {
		return f, err
	}
	return f.Normalize(), nil
}

// parseEventFilter reads an EventFilter from query parameters. Same
// conventions as parseSessionFilter, plus:
//
//	session_id   restrict to one session
//	kind         repeatable; event kinds to include
func parseEventFilter(r *http.Request) (EventFilter, error) {
	q := r.URL.Query()
	f := EventFilter{
		SessionID:  session.ID(q.Get("session_id")),
		Principal:  q.Get("principal"),
		Connection: q.Get("connection"),
		Protocol:   inspect.Protocol(q.Get("protocol")),
		Search:     q.Get("q"),
		Cursor:     q.Get("cursor"),
	}
	for _, k := range q["kind"] {
		if k == "" {
			continue
		}
		f.Kinds = append(f.Kinds, audit.Kind(k))
	}

	var err error
	if f.Since, err = parseTime(q.Get("since")); err != nil {
		return f, errors.New("invalid 'since': " + err.Error())
	}
	if f.Until, err = parseTime(q.Get("until")); err != nil {
		return f, errors.New("invalid 'until': " + err.Error())
	}
	if f.DeniedOnly, err = parseBool(q.Get("denied")); err != nil {
		return f, errors.New("invalid 'denied': " + err.Error())
	}
	if f.Limit, err = parseLimit(q.Get("limit")); err != nil {
		return f, err
	}
	return f.Normalize(), nil
}

// parseTime accepts RFC3339, or a bare date (2026-07-28) for the common case
// of an operator typing a day into a URL.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, errors.New("want RFC3339 or YYYY-MM-DD")
	}
	return t.UTC(), nil
}

func parseBool(s string) (bool, error) {
	if s == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return false, errors.New("want true or false")
	}
	return v, nil
}

func parseLimit(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, errors.New("invalid 'limit': want an integer")
	}
	if n < 0 {
		return 0, errors.New("invalid 'limit': must not be negative")
	}
	// Normalize clamps to MaxLimit rather than erroring: a caller asking for
	// more than the cap gets the cap, which is friendlier than a 400 and
	// still bounded.
	return n, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	// Audit data is full of SQL; escaping > and & into \u003e makes it
	// unreadable in a terminal and unsearchable in a browser's devtools.
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
