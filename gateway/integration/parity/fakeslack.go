//go:build integration && parity

package parity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const fakeSlackTeamID = "TPARITY"

// FakeSlack answers the Slack Web API calls the gateway makes and plays the
// Socket Mode side of Slack. The gateway reaches it through SLACK_API_URL:
// apps.connections.open hands out the fake's own websocket, and a click is an
// "interactive" envelope sent down it, as Slack sends one.
type FakeSlack struct {
	srv *httptest.Server

	mu         sync.Mutex
	seq        int
	users      map[string]FakeSlackUser
	posted     []SlackMessage
	updated    []SlackMessage
	ephemerals []SlackEphemeral
	views      []slackView
	unhandled  []string
	acks       map[string]bool
	sockets    int

	// socket is the websocket the gateway holds now. A reconnect replaces it.
	wsMu   sync.Mutex
	socket *websocket.Conn
	conns  []*websocket.Conn
}

// FakeSlackUser is what users.info answers for one Slack user.
type FakeSlackUser struct {
	ID     string
	Name   string
	Email  string
	TeamID string
	// Guest is a Slack single or multi channel guest (is_restricted).
	Guest   bool
	Deleted bool
	IsBot   bool
}

// SlackMessage is one message the gateway posted or rewrote.
type SlackMessage struct {
	// Method is chat.postMessage, chat.update or response_url.
	Method   string
	Channel  string
	TS       string
	Text     string
	Blocks   json.RawMessage
	Metadata slackMetadata
}

type slackMetadata struct {
	EventType    string         `json:"event_type"`
	EventPayload map[string]any `json:"event_payload"`
}

// ReviewID is the review the message was posted for, from its metadata.
func (m SlackMessage) ReviewID() string { return m.payloadString("review_id") }

// SessionID is the session the message was posted for, from its metadata.
func (m SlackMessage) SessionID() string { return m.payloadString("session_id") }

func (m SlackMessage) payloadString(key string) string {
	if v, ok := m.Metadata.EventPayload[key].(string); ok {
		return v
	}
	return ""
}

// Shows reports whether s appears in the rendered blocks or the text.
func (m SlackMessage) Shows(s string) bool {
	return strings.Contains(string(m.Blocks), s) || strings.Contains(m.Text, s)
}

// SlackEphemeral is a message only one user of a channel sees.
type SlackEphemeral struct {
	Channel string
	User    string
	Text    string
}

type slackView struct {
	TriggerID       string
	CallbackID      string
	PrivateMetadata string
}

// slackButton is one button of an action block, as the gateway posted it.
type slackButton struct {
	BlockID  string
	ActionID string
	Value    string
	Text     json.RawMessage
}

func newFakeSlack() (*FakeSlack, error) {
	f := &FakeSlack{users: map[string]FakeSlackUser{}, acks: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		writeSlack(w, map[string]any{"ok": true, "team_id": fakeSlackTeamID, "user_id": "UBOT", "bot_id": "BPARITY"})
	})
	mux.HandleFunc("/apps.connections.open", func(w http.ResponseWriter, _ *http.Request) {
		writeSlack(w, map[string]any{"ok": true, "url": "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/socket"})
	})
	mux.HandleFunc("/socket", f.serveSocket)
	mux.HandleFunc("/chat.postMessage", f.chatPostMessage)
	mux.HandleFunc("/chat.update", f.chatUpdate)
	mux.HandleFunc("/chat.postEphemeral", f.chatPostEphemeral)
	mux.HandleFunc("/users.info", f.usersInfo)
	mux.HandleFunc("/views.open", f.viewsOpen)
	mux.HandleFunc("/response", f.responseURL)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.unhandled = append(f.unhandled, r.URL.Path)
		f.mu.Unlock()
		writeSlack(w, map[string]any{"ok": false, "error": "unknown_method"})
	})
	f.srv = httptest.NewServer(mux)
	return f, nil
}

// URL is the base the gateway joins method names to.
func (f *FakeSlack) URL() string { return f.srv.URL + "/" }

func (f *FakeSlack) Close() {
	f.wsMu.Lock()
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.wsMu.Unlock()
	f.srv.Close()
}

func writeSlack(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// AddUser makes users.info answer for u. A later call for the same id wins.
func (f *FakeSlack) AddUser(u FakeSlackUser) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.ID] = u
}

// Posted returns every chat.postMessage call so far.
func (f *FakeSlack) Posted() []SlackMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SlackMessage(nil), f.posted...)
}

// Updated returns every rewrite of a message so far: chat.update calls and
// posts to an interaction's response_url.
func (f *FakeSlack) Updated() []SlackMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SlackMessage(nil), f.updated...)
}

// Ephemerals returns every chat.postEphemeral call so far.
func (f *FakeSlack) Ephemerals() []SlackEphemeral {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SlackEphemeral(nil), f.ephemerals...)
}

// Diagnostics summarizes what the fake saw, for a failure message.
func (f *FakeSlack) Diagnostics() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprintf("sockets=%d posted=%d updated=%d ephemerals=%d unhandled=%v",
		f.sockets, len(f.posted), len(f.updated), len(f.ephemerals), f.unhandled)
}

// ReviewMessages returns the messages posted for the review, one per channel.
func (f *FakeSlack) ReviewMessages(reviewID string) []SlackMessage {
	var out []SlackMessage
	for _, m := range f.Posted() {
		if m.ReviewID() == reviewID {
			out = append(out, m)
		}
	}
	return out
}

// WaitSocket waits until the gateway holds a Socket Mode connection.
func (f *FakeSlack) WaitSocket(ctx context.Context, timeout time.Duration) error {
	return waitFor(ctx, timeout, func() (bool, error) {
		f.wsMu.Lock()
		defer f.wsMu.Unlock()
		return f.socket != nil, nil
	})
}

func (f *FakeSlack) nextID(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	return fmt.Sprintf("%s%06d", prefix, f.seq)
}

func (f *FakeSlack) nextTS() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	return fmt.Sprintf("%d.%06d", time.Now().Unix(), f.seq)
}

func (f *FakeSlack) chatPostMessage(w http.ResponseWriter, r *http.Request) {
	m, err := formMessage(r, "chat.postMessage")
	if err != nil {
		writeSlack(w, map[string]any{"ok": false, "error": "invalid_form_data"})
		return
	}
	m.TS = f.nextTS()
	f.mu.Lock()
	f.posted = append(f.posted, m)
	f.mu.Unlock()
	writeSlack(w, map[string]any{"ok": true, "channel": m.Channel, "ts": m.TS,
		"message": map[string]any{"type": "message", "ts": m.TS, "text": m.Text}})
}

func (f *FakeSlack) chatUpdate(w http.ResponseWriter, r *http.Request) {
	m, err := formMessage(r, "chat.update")
	if err != nil {
		writeSlack(w, map[string]any{"ok": false, "error": "invalid_form_data"})
		return
	}
	m.TS = r.PostForm.Get("ts")
	f.mu.Lock()
	f.updated = append(f.updated, m)
	f.mu.Unlock()
	writeSlack(w, map[string]any{"ok": true, "channel": m.Channel, "ts": m.TS, "text": m.Text})
}

func formMessage(r *http.Request, method string) (SlackMessage, error) {
	if err := r.ParseForm(); err != nil {
		return SlackMessage{}, err
	}
	m := SlackMessage{
		Method:  method,
		Channel: r.PostForm.Get("channel"),
		Text:    r.PostForm.Get("text"),
		Blocks:  json.RawMessage(r.PostForm.Get("blocks")),
	}
	if meta := r.PostForm.Get("metadata"); meta != "" {
		if err := json.Unmarshal([]byte(meta), &m.Metadata); err != nil {
			return SlackMessage{}, err
		}
	}
	return m, nil
}

// responseURL takes the rewrite the gateway sends to an interaction's
// response_url, which carries the message it replaces in the query.
func (f *FakeSlack) responseURL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text   string          `json:"text"`
		Blocks json.RawMessage `json:"blocks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeSlack(w, map[string]any{"ok": false, "error": "invalid_json"})
		return
	}
	f.mu.Lock()
	f.updated = append(f.updated, SlackMessage{Method: "response_url", Channel: r.URL.Query().Get("channel"),
		TS: r.URL.Query().Get("ts"), Text: body.Text, Blocks: body.Blocks})
	f.mu.Unlock()
	writeSlack(w, map[string]any{"ok": true})
}

func (f *FakeSlack) chatPostEphemeral(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeSlack(w, map[string]any{"ok": false, "error": "invalid_form_data"})
		return
	}
	f.mu.Lock()
	f.ephemerals = append(f.ephemerals, SlackEphemeral{Channel: r.PostForm.Get("channel"),
		User: r.PostForm.Get("user"), Text: r.PostForm.Get("text")})
	f.mu.Unlock()
	writeSlack(w, map[string]any{"ok": true, "message_ts": f.nextTS()})
}

func (f *FakeSlack) usersInfo(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeSlack(w, map[string]any{"ok": false, "error": "invalid_form_data"})
		return
	}
	f.mu.Lock()
	u, ok := f.users[r.Form.Get("user")]
	f.mu.Unlock()
	if !ok {
		writeSlack(w, map[string]any{"ok": false, "error": "user_not_found"})
		return
	}
	team := u.TeamID
	if team == "" {
		team = fakeSlackTeamID
	}
	writeSlack(w, map[string]any{"ok": true, "user": map[string]any{
		"id": u.ID, "team_id": team, "name": u.Name, "real_name": u.Name,
		"deleted": u.Deleted, "is_bot": u.IsBot,
		"is_restricted": u.Guest, "is_ultra_restricted": false, "is_stranger": false,
		"is_email_confirmed": true,
		"profile":            map[string]any{"email": u.Email, "real_name": u.Name, "display_name": u.Name},
	}})
}

func (f *FakeSlack) viewsOpen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TriggerID string `json:"trigger_id"`
		View      struct {
			CallbackID      string `json:"callback_id"`
			PrivateMetadata string `json:"private_metadata"`
		} `json:"view"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeSlack(w, map[string]any{"ok": false, "error": "invalid_json"})
		return
	}
	f.mu.Lock()
	f.views = append(f.views, slackView{TriggerID: body.TriggerID, CallbackID: body.View.CallbackID,
		PrivateMetadata: body.View.PrivateMetadata})
	f.mu.Unlock()
	writeSlack(w, map[string]any{"ok": true, "view": map[string]any{"id": f.nextID("V"),
		"callback_id": body.View.CallbackID, "private_metadata": body.View.PrivateMetadata}})
}

var slackUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// serveSocket is Slack's side of Socket Mode: hello, a ping well inside the
// client's 30s dead-man interval, and the acks the gateway sends back.
func (f *FakeSlack) serveSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := slackUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.wsMu.Lock()
	err = conn.WriteJSON(map[string]any{"type": "hello", "num_connections": 1,
		"connection_info": map[string]any{"app_id": "APARITY"},
		"debug_info":      map[string]any{"host": "parity", "approximate_connection_time": 3600}})
	if err != nil {
		f.wsMu.Unlock()
		_ = conn.Close()
		return
	}
	f.socket = conn
	f.conns = append(f.conns, conn)
	f.wsMu.Unlock()
	f.mu.Lock()
	f.sockets++
	f.mu.Unlock()

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte("parity"), time.Now().Add(5*time.Second)); err != nil {
					return
				}
			}
		}
	}()
	defer close(done)
	for {
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if err := conn.ReadJSON(&ack); err != nil {
			f.wsMu.Lock()
			if f.socket == conn {
				f.socket = nil
			}
			f.wsMu.Unlock()
			_ = conn.Close()
			return
		}
		if ack.EnvelopeID != "" {
			f.mu.Lock()
			f.acks[ack.EnvelopeID] = true
			f.mu.Unlock()
		}
	}
}

// sendInteractive sends one interaction to the gateway and waits for its ack.
func (f *FakeSlack) sendInteractive(ctx context.Context, payload map[string]any) error {
	envelopeID := f.nextID("env-")
	if err := f.WaitSocket(ctx, 30*time.Second); err != nil {
		return fmt.Errorf("the gateway holds no Socket Mode connection: %w (%s)", err, f.Diagnostics())
	}
	f.wsMu.Lock()
	conn := f.socket
	if conn == nil {
		f.wsMu.Unlock()
		return fmt.Errorf("the gateway dropped its Socket Mode connection (%s)", f.Diagnostics())
	}
	err := conn.WriteJSON(map[string]any{"envelope_id": envelopeID, "type": "interactive",
		"payload": payload, "accepts_response_payload": payload["type"] == "view_submission"})
	f.wsMu.Unlock()
	if err != nil {
		return fmt.Errorf("sending the interaction: %w", err)
	}
	err = waitFor(ctx, 15*time.Second, func() (bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.acks[envelopeID], nil
	})
	if err != nil {
		return fmt.Errorf("the gateway did not ack envelope %s: %w", envelopeID, err)
	}
	return nil
}

// buttons lists the buttons of every action block of m.
func (m SlackMessage) buttons() ([]slackButton, error) {
	var blocks []struct {
		Type     string `json:"type"`
		BlockID  string `json:"block_id"`
		Elements []struct {
			Type     string          `json:"type"`
			ActionID string          `json:"action_id"`
			Value    string          `json:"value"`
			Text     json.RawMessage `json:"text"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(m.Blocks, &blocks); err != nil {
		return nil, fmt.Errorf("decoding the message blocks: %w", err)
	}
	var out []slackButton
	for _, b := range blocks {
		if b.Type != "actions" {
			continue
		}
		for _, e := range b.Elements {
			if e.Type == "button" {
				out = append(out, slackButton{BlockID: b.BlockID, ActionID: e.ActionID, Value: e.Value, Text: e.Text})
			}
		}
	}
	return out, nil
}

// Button returns the button with actionID of the approver group, the way a
// reviewer sees it in the posted message.
func (m SlackMessage) Button(actionID, group string) (slackButton, error) {
	buttons, err := m.buttons()
	if err != nil {
		return slackButton{}, err
	}
	for _, b := range buttons {
		parts := strings.Split(b.BlockID, ":")
		if b.ActionID == actionID && len(parts) == 3 && parts[1] == group {
			return b, nil
		}
	}
	return slackButton{}, fmt.Errorf("message %s on %s has no %s button for group %q: %s",
		m.TS, m.Channel, actionID, group, truncate(m.Blocks))
}

// clickPayload is the block_actions payload Slack sends when userID clicks b
// on m.
func (f *FakeSlack) clickPayload(m SlackMessage, b slackButton, userID string) map[string]any {
	f.mu.Lock()
	u := f.users[userID]
	f.mu.Unlock()
	name := u.Name
	if name == "" {
		name = strings.ToLower(userID)
	}
	team := u.TeamID
	if team == "" {
		team = fakeSlackTeamID
	}
	actionTS := f.nextTS()
	return map[string]any{
		"type":       "block_actions",
		"token":      "parity-verification-token",
		"api_app_id": "APARITY",
		"trigger_id": f.nextID("trigger-"),
		"user":       map[string]any{"id": userID, "username": name, "name": name, "team_id": team},
		"team":       map[string]any{"id": team, "domain": "parity"},
		"container": map[string]any{"type": "message", "message_ts": m.TS, "channel_id": m.Channel,
			"is_ephemeral": false},
		"channel": map[string]any{"id": m.Channel, "name": strings.ToLower(m.Channel)},
		"message": map[string]any{"type": "message", "subtype": "bot_message", "bot_id": "BPARITY",
			"ts": m.TS, "text": m.Text, "blocks": m.Blocks, "metadata": m.Metadata},
		"response_url": f.srv.URL + "/response?channel=" + m.Channel + "&ts=" + m.TS,
		"actions": []map[string]any{{"type": "button", "action_id": b.ActionID, "block_id": b.BlockID,
			"value": b.Value, "text": b.Text, "action_ts": actionTS}},
		"state": map[string]any{"values": map[string]any{}},
	}
}

// Approve clicks the Approve button of group on m as userID.
func (f *FakeSlack) Approve(ctx context.Context, m SlackMessage, userID, group string) error {
	b, err := m.Button("review-approved", group)
	if err != nil {
		return err
	}
	return f.sendInteractive(ctx, f.clickPayload(m, b, userID))
}

// Reject clicks the Reject button of group on m as userID, waits for the
// reason modal the gateway opens, and submits it with reason.
func (f *FakeSlack) Reject(ctx context.Context, m SlackMessage, userID, group, reason string) error {
	b, err := m.Button("review-rejected", group)
	if err != nil {
		return err
	}
	click := f.clickPayload(m, b, userID)
	if err := f.sendInteractive(ctx, click); err != nil {
		return err
	}
	triggerID := click["trigger_id"].(string)
	var view slackView
	err = waitFor(ctx, 15*time.Second, func() (bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, v := range f.views {
			if v.TriggerID == triggerID {
				view = v
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("the gateway opened no reject modal for trigger %s: %w", triggerID, err)
	}
	return f.sendInteractive(ctx, map[string]any{
		"type":       "view_submission",
		"token":      "parity-verification-token",
		"api_app_id": "APARITY",
		"trigger_id": f.nextID("trigger-"),
		"user":       click["user"],
		"team":       click["team"],
		"view": map[string]any{
			"id": f.nextID("V"), "type": "modal", "callback_id": view.CallbackID,
			"private_metadata": view.PrivateMetadata, "team_id": fakeSlackTeamID, "app_id": "APARITY",
			"state": map[string]any{"values": map[string]any{
				"rejection_reason_block": map[string]any{
					"rejection_reason": map[string]any{"type": "plain_text_input", "value": reason},
				},
			}},
		},
	})
}
