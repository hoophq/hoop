// Package slack is a twin of the Slack surface hoop uses: the Web API on
// slack.com, Socket Mode on the websocket URL apps.connections.open returns,
// and response_url callbacks on hooks.slack.com.
//
// It also acts as a Slack user: POST /_twin/act/click delivers a
// block_actions envelope over the open socket, as Slack does when a user
// clicks a button, and waits for the ack.
package slack

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/twins/kit"
)

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type seed struct {
	TeamID   string    `json:"team_id"`
	BotToken string    `json:"bot_token"`
	AppToken string    `json:"app_token"`
	Channels []Channel `json:"channels"`
	Users    []User    `json:"users"`
}

// Message is a posted message. Updates counts chat.update and response_url
// rewrites, so a scenario can see that hoop closed the review message.
type Message struct {
	Channel  string          `json:"channel"`
	TS       string          `json:"ts"`
	Text     string          `json:"text,omitempty"`
	Blocks   json.RawMessage `json:"blocks,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Updates  []string        `json:"updates,omitempty"`
}

type Ephemeral struct {
	Channel string `json:"channel"`
	User    string `json:"user"`
	Text    string `json:"text"`
}

type Envelope struct {
	EnvelopeID string    `json:"envelope_id"`
	Type       string    `json:"type"`
	User       string    `json:"user"`
	ActionID   string    `json:"action_id"`
	SentAt     time.Time `json:"sent_at"`
	AckedAt    time.Time `json:"acked_at,omitzero"`
}

type Twin struct {
	socketURL string
	hooksURL  string
	upgrader  websocket.Upgrader

	mu          sync.Mutex
	cfg         seed
	channels    map[string]Channel // by id
	users       map[string]User    // by id
	messages    []*Message
	dms         []*Message
	ephemerals  []Ephemeral
	modals      []json.RawMessage
	envelopes   []*Envelope
	responseURL map[string]*Message
	tickets     map[string]bool
	sockets     map[*websocket.Conn]*sync.Mutex
	opened      int // apps.connections.open calls
	hellos      int
	seq         int
	acks        map[string]chan struct{}
}

// New builds the twin. socketURL is what apps.connections.open returns and
// hooksURL the base of every response_url, so both name real Slack hosts.
func New(socketURL, hooksURL string) *Twin {
	t := &Twin{socketURL: socketURL, hooksURL: strings.TrimSuffix(hooksURL, "/"),
		// slack-go sends Origin: https://api.slack.com, as Slack expects.
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}}
	t.Reset()
	return t
}

func (t *Twin) Name() string { return "slack" }

func (t *Twin) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for c := range t.sockets {
		c.Close()
	}
	t.cfg = seed{TeamID: "T0TWIN", BotToken: "xoxb-twin", AppToken: "xapp-twin"}
	t.channels, t.users = map[string]Channel{}, map[string]User{}
	t.messages, t.dms, t.ephemerals, t.modals, t.envelopes = nil, nil, nil, nil, nil
	t.responseURL, t.tickets = map[string]*Message{}, map[string]bool{}
	t.sockets, t.acks = map[*websocket.Conn]*sync.Mutex{}, map[string]chan struct{}{}
	t.opened, t.hellos = 0, 0
}

func (t *Twin) Seed(raw json.RawMessage) error {
	var s seed
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if s.TeamID != "" {
		t.cfg.TeamID = s.TeamID
	}
	if s.BotToken != "" {
		t.cfg.BotToken = s.BotToken
	}
	if s.AppToken != "" {
		t.cfg.AppToken = s.AppToken
	}
	for _, c := range s.Channels {
		t.channels[c.ID] = c
	}
	for _, u := range s.Users {
		t.users[u.ID] = u
	}
	return nil
}

func (t *Twin) State() any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{
		"team_id": t.cfg.TeamID, "messages": t.messages, "dms": t.dms, "ephemerals": t.ephemerals,
		"modals": t.modals, "envelopes": t.envelopes,
		"socket": map[string]int{"connections_opened": t.opened, "hellos_sent": t.hellos, "open_now": len(t.sockets)},
	}
}

func (t *Twin) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth.test", t.botAuth(t.authTest))
	mux.HandleFunc("POST /api/apps.connections.open", t.connectionsOpen)
	mux.HandleFunc("POST /api/chat.postMessage", t.botAuth(t.postMessage))
	mux.HandleFunc("POST /api/chat.update", t.botAuth(t.update))
	mux.HandleFunc("POST /api/chat.postEphemeral", t.botAuth(t.postEphemeral))
	mux.HandleFunc("POST /api/users.info", t.botAuth(t.usersInfo))
	mux.HandleFunc("POST /api/views.open", t.botAuth(t.viewsOpen))
	mux.HandleFunc("POST /actions/", t.responseURLPost)
	mux.HandleFunc("GET /link/", t.socket)
}

func (t *Twin) Actions(mux *http.ServeMux) {
	mux.HandleFunc("POST /_twin/act/click", t.click)
}

// --- Web API ---

func token(r *http.Request) string {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return tok
	}
	return r.FormValue("token")
}

func apiError(w http.ResponseWriter, code string) {
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": false, "error": code})
}

func (t *Twin) botAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		t.mu.Lock()
		ok := token(r) == t.cfg.BotToken
		t.mu.Unlock()
		if !ok {
			apiError(w, "invalid_auth")
			return
		}
		next(w, r)
	}
}

func (t *Twin) authTest(w http.ResponseWriter, _ *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "url": "https://twins.slack.com/",
		"team": "twins", "user": "hoop", "team_id": t.cfg.TeamID, "user_id": "U0BOT", "bot_id": "B0BOT"})
}

func (t *Twin) connectionsOpen(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if token(r) != t.cfg.AppToken {
		apiError(w, "invalid_auth")
		return
	}
	ticket := random(16)
	t.tickets[ticket] = true
	t.opened++
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "url": t.socketURL + "?ticket=" + ticket + "&app_id=A0TWIN"})
}

// channelID resolves an ID, a name or a #name. A user ID is a DM.
func (t *Twin) channelID(ref string) (id string, dm, ok bool) {
	if _, ok := t.users[ref]; ok {
		return ref, true, true
	}
	if _, ok := t.channels[ref]; ok {
		return ref, false, true
	}
	name := strings.TrimPrefix(ref, "#")
	for _, c := range t.channels {
		if c.Name == name {
			return c.ID, false, true
		}
	}
	return "", false, false
}

func (t *Twin) postMessage(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	id, dm, ok := t.channelID(r.FormValue("channel"))
	if !ok {
		apiError(w, "channel_not_found")
		return
	}
	m := &Message{Channel: id, TS: t.nextTS(), Text: r.FormValue("text"),
		Blocks: rawOrNil(r.FormValue("blocks")), Metadata: rawOrNil(r.FormValue("metadata"))}
	if dm {
		t.dms = append(t.dms, m)
	} else {
		t.messages = append(t.messages, m)
	}
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": id, "ts": m.TS,
		"message": map[string]any{"type": "message", "ts": m.TS, "text": m.Text}})
}

func (t *Twin) update(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.find(r.FormValue("channel"), r.FormValue("ts"))
	if m == nil {
		apiError(w, "message_not_found")
		return
	}
	if b := r.FormValue("blocks"); b != "" {
		m.Blocks = json.RawMessage(b)
	}
	m.Updates = append(m.Updates, "chat.update")
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "channel": m.Channel, "ts": m.TS})
}

func (t *Twin) postEphemeral(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := Ephemeral{Channel: r.FormValue("channel"), User: r.FormValue("user"), Text: r.FormValue("text")}
	if e.Text == "" {
		e.Text = r.FormValue("blocks")
	}
	t.ephemerals = append(t.ephemerals, e)
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "message_ts": t.nextTS()})
}

func (t *Twin) usersInfo(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	u, ok := t.users[r.FormValue("user")]
	if !ok {
		apiError(w, "user_not_found")
		return
	}
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "user": map[string]any{
		"id": u.ID, "team_id": t.cfg.TeamID, "name": u.Name, "is_email_confirmed": true,
		"profile": map[string]any{"email": u.Email, "real_name": u.Name}}})
}

func (t *Twin) viewsOpen(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apiError(w, "invalid_json")
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.modals = append(t.modals, body)
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "view": map[string]any{"id": "V" + random(8)}})
}

// responseURLPost rewrites the message a response_url was issued for.
func (t *Twin) responseURLPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReplaceOriginal bool            `json:"replace_original"`
		Text            string          `json:"text"`
		Blocks          json.RawMessage `json:"blocks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.responseURL[r.URL.Path]
	if !ok {
		http.Error(w, "unknown response_url", http.StatusNotFound)
		return
	}
	if body.ReplaceOriginal && len(body.Blocks) > 0 && string(body.Blocks) != "null" {
		m.Blocks = body.Blocks
	}
	m.Updates = append(m.Updates, "response_url")
	kit.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Socket Mode ---

func (t *Twin) socket(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	valid := t.tickets[r.URL.Query().Get("ticket")]
	delete(t.tickets, r.URL.Query().Get("ticket"))
	t.mu.Unlock()
	if !valid {
		http.Error(w, "unknown ticket", http.StatusUnauthorized)
		return
	}
	conn, err := t.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[slack] websocket upgrade: %v", err)
		return
	}
	wmu := &sync.Mutex{}
	t.mu.Lock()
	t.sockets[conn] = wmu
	t.hellos++
	n := len(t.sockets)
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.sockets, conn)
		t.mu.Unlock()
		conn.Close()
		log.Printf("[slack] socket closed")
	}()
	if err := t.write(conn, wmu, map[string]any{"type": "hello", "num_connections": n,
		"connection_info": map[string]string{"app_id": "A0TWIN"}}); err != nil {
		return
	}
	log.Printf("[slack] socket open, hello sent")
	done := make(chan struct{})
	defer close(done)
	go func() {
		// slack-go drops the connection when Slack sends no PING for 30s.
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				wmu.Lock()
				err := conn.WriteControl(websocket.PingMessage, []byte("twin"), time.Now().Add(5*time.Second))
				wmu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if json.Unmarshal(raw, &ack) != nil || ack.EnvelopeID == "" {
			continue
		}
		t.mu.Lock()
		for _, e := range t.envelopes {
			if e.EnvelopeID == ack.EnvelopeID {
				e.AckedAt = time.Now().UTC()
			}
		}
		if ch, ok := t.acks[ack.EnvelopeID]; ok {
			close(ch)
			delete(t.acks, ack.EnvelopeID)
		}
		t.mu.Unlock()
		log.Printf("[slack] ack %s", ack.EnvelopeID)
	}
}

func (t *Twin) write(conn *websocket.Conn, wmu *sync.Mutex, v any) error {
	wmu.Lock()
	defer wmu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return conn.WriteJSON(v)
}

// --- the twin as a Slack user ---

type clickRequest struct {
	User     string `json:"user"`
	ActionID string `json:"action_id"` // review-approved or review-rejected
	Group    string `json:"group"`     // the approver group block; empty takes the first
	TS       string `json:"ts"`        // empty takes the newest message with buttons
}

func (t *Twin) click(w http.ResponseWriter, r *http.Request) {
	var req clickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.User == "" || req.ActionID == "" {
		http.Error(w, "want {user, action_id, group?, ts?}", http.StatusBadRequest)
		return
	}
	t.mu.Lock()
	u, ok := t.users[req.User]
	if !ok {
		t.mu.Unlock()
		http.Error(w, "unknown user", http.StatusBadRequest)
		return
	}
	m, blockID, value := t.findButton(req)
	if m == nil {
		t.mu.Unlock()
		http.Error(w, "no message with that button", http.StatusConflict)
		return
	}
	var conn *websocket.Conn
	var wmu *sync.Mutex
	for c, l := range t.sockets {
		conn, wmu = c, l
		break
	}
	if conn == nil {
		t.mu.Unlock()
		http.Error(w, "no socket open: hoop is not connected", http.StatusConflict)
		return
	}
	t.seq++
	respPath := fmt.Sprintf("/actions/%s/%d/%s", t.cfg.TeamID, t.seq, random(12))
	t.responseURL[respPath] = m
	ch := t.channels[m.Channel]
	var meta any
	_ = json.Unmarshal(m.Metadata, &meta)
	var blocks any
	_ = json.Unmarshal(m.Blocks, &blocks)
	payload := map[string]any{
		"type":         "block_actions",
		"team":         map[string]string{"id": t.cfg.TeamID, "domain": "twins"},
		"user":         map[string]string{"id": u.ID, "username": u.Name, "name": u.Name, "team_id": t.cfg.TeamID},
		"api_app_id":   "A0TWIN",
		"trigger_id":   random(20),
		"container":    map[string]any{"type": "message", "message_ts": m.TS, "channel_id": m.Channel},
		"channel":      map[string]string{"id": ch.ID, "name": ch.Name},
		"message":      map[string]any{"type": "message", "ts": m.TS, "text": m.Text, "blocks": blocks, "metadata": meta, "bot_id": "B0BOT"},
		"response_url": t.hooksURL + respPath,
		"actions": []map[string]any{{"type": "button", "action_id": req.ActionID, "block_id": blockID,
			"value": value, "action_ts": t.nextTS()}},
	}
	env := &Envelope{EnvelopeID: random(16), Type: "interactive", User: u.ID, ActionID: req.ActionID, SentAt: time.Now().UTC()}
	t.envelopes = append(t.envelopes, env)
	acked := make(chan struct{})
	t.acks[env.EnvelopeID] = acked
	t.mu.Unlock()

	err := t.write(conn, wmu, map[string]any{"envelope_id": env.EnvelopeID, "type": "interactive",
		"accepts_response_payload": true, "payload": payload})
	if err != nil {
		http.Error(w, "socket write: "+err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("[slack] %s clicked %s on %s (block %s), envelope %s", u.ID, req.ActionID, m.TS, blockID, env.EnvelopeID)
	select {
	case <-acked:
		kit.WriteJSON(w, http.StatusOK, map[string]any{"envelope_id": env.EnvelopeID, "acked": true})
	case <-time.After(5 * time.Second):
		kit.WriteJSON(w, http.StatusGatewayTimeout, map[string]any{"envelope_id": env.EnvelopeID, "acked": false})
	}
}

// findButton returns the message holding the clicked button, the button's
// block_id and value. hoop's block_id is <reviewID>:<group>:<idx>.
func (t *Twin) findButton(req clickRequest) (*Message, string, string) {
	for i := len(t.messages) - 1; i >= 0; i-- {
		m := t.messages[i]
		if req.TS != "" && m.TS != req.TS {
			continue
		}
		var blocks []struct {
			Type     string `json:"type"`
			BlockID  string `json:"block_id"`
			Elements []struct {
				ActionID string `json:"action_id"`
				Value    string `json:"value"`
			} `json:"elements"`
		}
		if json.Unmarshal(m.Blocks, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "actions" {
				continue
			}
			if parts := strings.Split(b.BlockID, ":"); req.Group != "" && (len(parts) != 3 || parts[1] != req.Group) {
				continue
			}
			for _, e := range b.Elements {
				if e.ActionID == req.ActionID {
					return m, b.BlockID, e.Value
				}
			}
		}
	}
	return nil, "", ""
}

func (t *Twin) find(channel, ts string) *Message {
	for _, m := range append(t.messages, t.dms...) {
		if m.Channel == channel && m.TS == ts {
			return m
		}
	}
	return nil
}

func (t *Twin) nextTS() string {
	t.seq++
	return fmt.Sprintf("%d.%06d", time.Now().Unix(), t.seq)
}

func rawOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
