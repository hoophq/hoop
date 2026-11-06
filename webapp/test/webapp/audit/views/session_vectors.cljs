(ns webapp.audit.views.session-vectors
  "Inputs and expectations of the session viewer tests. The tests and
  session-fixtures both read them; session-fixtures writes them as the JSON
  fixtures of webapp_v2 (npm run fixtures:sessions)."
  (:require
   [webapp.audit.views.session-format :as session-format]
   [webapp.audit.views.session-live-tail :as live-tail]
   [webapp.audit.views.ssh-decoder :as ssh-decoder]
   [webapp.audit.views.terminal-decoder :as terminal-decoder]))

;; ─── Builders ──────────────────────────────────────────────────────────────

(defn b64 [bytes]
  (js/btoa (apply str (map #(js/String.fromCharCode %) bytes))))

(defn text-bytes [s] (map #(.charCodeAt s %) (range (count s))))

(defn- uint32 [n] [(bit-and (unsigned-bit-shift-right n 24) 0xFF)
                   (bit-and (unsigned-bit-shift-right n 16) 0xFF)
                   (bit-and (unsigned-bit-shift-right n 8) 0xFF)
                   (bit-and n 0xFF)])

(defn- frame [frame-type channel & parts]
  (b64 (concat [frame-type (bit-and channel 0xFF) (bit-shift-right channel 8)]
               (apply concat parts))))

(defn- open [seconds channel channel-type]
  [seconds "i" (frame 1 channel (text-bytes channel-type) [0])])

(defn- request [seconds channel request-type payload]
  [seconds "i" (frame 2 channel (text-bytes request-type) [0 1] payload)])

(defn- pty-req [seconds channel cols rows]
  (request seconds channel "pty-req"
           (concat (uint32 5) (text-bytes "xterm") (uint32 cols) (uint32 rows)
                   (uint32 0) (uint32 0) (uint32 0))))

(defn- window-change [seconds channel cols rows]
  (request seconds channel "window-change"
           (concat (uint32 cols) (uint32 rows) (uint32 0) (uint32 0))))

(defn- data [seconds event-type channel s]
  [seconds event-type (frame 3 channel (text-bytes s))])

(defn- close [seconds channel]
  [seconds "o" (frame 4 channel (text-bytes "session"))])

;; ─── session-format/recording-format ───────────────────────────────────────

(def format-cases
  [{:test "historical-sessions-keep-their-viewer"
    :name "custom kubernetes without a format is a pty"
    :session {:type "custom" :connection_subtype "kubernetes"}
    :expected "pty"}
   {:test "historical-sessions-keep-their-viewer"
    :name "custom rdp without a format is rdp"
    :session {:type "custom" :connection_subtype "rdp"}
    :expected "rdp"}
   {:test "historical-sessions-keep-their-viewer"
    :name "application without a subtype is a pty"
    :session {:type "application"}
    :expected "pty"}
   {:test "historical-ssh-sessions-use-the-ssh-viewer"
    :name "application ssh is ssh"
    :session {:type "application" :connection_subtype "ssh"}
    :expected "ssh"}
   {:test "historical-ssh-sessions-use-the-ssh-viewer"
    :name "application ssh-local is ssh"
    :session {:type "application" :connection_subtype "ssh-local"}
    :expected "ssh"}
   {:test "historical-ssh-sessions-use-the-ssh-viewer"
    :name "application git is ssh"
    :session {:type "application" :connection_subtype "git"}
    :expected "ssh"}
   {:test "historical-ssh-sessions-use-the-ssh-viewer"
    :name "application github is ssh"
    :session {:type "application" :connection_subtype "github"}
    :expected "ssh"}
   {:test "historical-ssh-sessions-use-the-ssh-viewer"
    :name "custom ssh is a pty"
    :session {:type "custom" :connection_subtype "ssh"}
    :expected "pty"}
   {:test "persisted-format-overrides-legacy-type"
    :name "a persisted raw format wins over custom kubernetes"
    :session {:type "custom" :connection_subtype "kubernetes" :recording_format "raw"}
    :expected "raw"}
   {:test "persisted-format-overrides-legacy-type"
    :name "a persisted pty format wins over custom"
    :session {:type "custom" :recording_format "pty"}
    :expected "pty"}])

(defn run-format [{:keys [session]}]
  (session-format/recording-format session))

;; ─── terminal-decoder/decode-events ────────────────────────────────────────

(def terminal-cases
  [{:test "output-character-split-across-events"
    :name "an output character split across events"
    :events [[0 "o" (b64 [0xE2])]
             [1 "o" (b64 [0x82 0xAC])]]
    :finalize? true
    :expected [[0 "o" ""] [1 "o" "€"]]}
   {:test "input-must-not-contaminate-output"
    :name "input does not complete an output character"
    :events [[0 "i" (b64 [0xE2])]
             [1 "o" (b64 [0x41])]
             [2 "i" (b64 [0x82 0xAC])]
             [3 "o" (b64 [0xE2])]
             [4 "e" (b64 [0x82 0xAC])]]
    :finalize? true
    :expected [[0 "i" ""] [1 "o" "A"] [2 "i" "€"]
               [3 "o" ""] [4 "e" "€"]]}
   {:test "incomplete-final-output-is-visible"
    :name "an incomplete final character shows as U+FFFD"
    :events [[0 "o" (b64 [0xE2])]]
    :finalize? true
    :expected [[0 "o" "�"]]}
   {:test "live-output-waits-for-the-rest-of-a-character"
    :name "live output holds the first byte of a character"
    :events [[0 "o" (b64 [0xE2])]]
    :finalize? false
    :expected [[0 "o" ""]]}
   {:test "live-output-waits-for-the-rest-of-a-character"
    :name "live output completes the character with the next event"
    :events [[0 "o" (b64 [0xE2])]
             [1 "o" (b64 [0x82 0xAC])]]
    :finalize? false
    :expected [[0 "o" ""] [1 "o" "€"]]}])

(defn run-terminal [{:keys [events finalize?]}]
  (terminal-decoder/decode-events events finalize?))

;; ─── ssh-decoder/terminal-recording ────────────────────────────────────────
;; :expected is nil when the stream is not SSH frames. Otherwise it holds the
;; keys the test checks, among those of ssh-result.

(def ssh-cases
  [{:test "interactive-shell-replays-terminal-bytes"
    :name "an interactive shell replays its terminal bytes"
    :events [(open 0 1 "session")
             (pty-req 0.1 1 120 40)
             (request 0.2 1 "shell" [])
             [0.3 "o" (frame 5 1 [1])]
             (data 0.4 "o" 1 "$ ")
             (data 0.5 "i" 1 "d")
             (data 0.6 "o" 1 "d")
             (close 0.7 1)
             [0.8 "e" (b64 (text-bytes "connection closed"))]]
    :expected {:terminal? true
               :size [120 40]
               :texts [[0.4 "o" "$ "] [0.5 "i" "d"] [0.6 "o" "d"] [0.8 "e" "connection closed"]]}}
   {:test "window-change-resizes-the-terminal"
    :name "a window-change resizes the terminal"
    :events [(open 0 1 "session")
             (pty-req 0 1 80 24)
             (window-change 1 1 200 50)
             (data 2 "o" 1 "x")]
    :expected {:size [80 24]
               :texts [[1 "r" "200x50"] [2 "o" "x"]]}}
   {:test "only-pty-channels-reach-the-terminal"
    :name "only pty channels reach the terminal"
    :events [(open 0 1 "session")
             (pty-req 0 1 80 24)
             (open 1 2 "direct-tcpip")
             (data 2 "i" 2 "GET / HTTP/1.1")
             (open 3 3 "session")
             (request 3 3 "subsystem" (text-bytes "sftp"))
             (data 4 "o" 3 "sftp bytes")
             (data 5 "o" 1 "prompt")]
    :expected {:terminal? true
               :texts [[5 "o" "prompt"]]}}
   {:test "a-reused-channel-id-starts-clean"
    :name "a reused channel id starts clean"
    ;; A second SSH connection of the session reuses channel 1.
    :events [(open 0 1 "session")
             (pty-req 0 1 80 24)
             (data 1 "o" 1 "first")
             (close 2 1)
             (open 3 1 "direct-tcpip")
             (data 4 "o" 1 "forwarded")]
    :expected {:terminal? true
               :texts [[1 "o" "first"]]}}
   {:test "overlapping-connections-are-not-replayed"
    :name "overlapping connections are not replayed"
    ;; A second connection opens channel 1 while the first one still uses it:
    ;; the stream cannot tell their bytes apart.
    :events [(open 0 1 "session")
             (pty-req 0 1 80 24)
             (open 1 1 "session")
             (request 1 1 "exec" (concat (uint32 3) (text-bytes "scp")))
             (data 2 "o" 1 "which one?")]
    :expected {:terminal? false}}
   {:test "concurrent-terminals-are-not-replayed"
    :name "concurrent terminals are not replayed"
    ;; One connection with two shells open at once (ssh ControlMaster).
    :events [(open 0 1 "session")
             (pty-req 0 1 80 24)
             (open 1 2 "session")
             (pty-req 1 2 80 24)
             (data 2 "o" 1 "first")
             (data 3 "o" 2 "second")]
    :expected {:terminal? false}}
   {:test "sequential-terminals-replay"
    :name "sequential terminals replay"
    ;; The second shell's pty-req sets its own size.
    :events [(open 0 1 "session")
             (pty-req 0 1 80 24)
             (data 1 "o" 1 "first")
             (close 2 1)
             (open 3 2 "session")
             (pty-req 3 2 80 24)
             (data 4 "o" 2 "second")]
    :expected {:terminal? true
               :texts [[1 "o" "first"] [3 "r" "80x24"] [4 "o" "second"]]}}
   {:test "a-channel-opened-before-the-stream-is-not-a-terminal"
    :name "a channel opened before the stream is not a terminal"
    ;; A live viewer subscribed after the channel was opened: nothing tells
    ;; whether it is a shell, an exec or a port forward.
    :events [(data 0 "o" 1 "$ ")
             (window-change 1 1 120 30)
             (data 2 "i" 1 "l")]
    :expected {:terminal? false
               :texts []}}
   {:test "a-channel-opened-before-the-stream-still-overlaps"
    :name "a channel opened before the stream still overlaps"
    :events [(data 0 "o" 1 "first connection")
             (open 1 1 "session")
             (pty-req 1 1 80 24)
             (data 2 "o" 1 "which one?")]
    :expected {:terminal? false}}
   {:test "a-new-connection-after-an-unopened-channel-closes-replays"
    :name "a new connection after an unopened channel closes replays"
    :events [(data 0 "o" 1 "first connection")
             (close 1 1)
             (data 2 "i" 1 "late keystroke")
             (open 3 1 "session")
             (pty-req 3 1 100 30)
             (data 4 "o" 1 "second")]
    :expected {:terminal? true
               :size [100 30]
               :texts [[4 "o" "second"]]}}
   {:test "sessions-without-a-pty-are-not-terminals"
    :name "a session without a pty is not a terminal"
    :events [(open 0 1 "session")
             (request 0 1 "exec" (concat (uint32 15) (text-bytes "git-upload-pack")))
             (data 1 "o" 1 "0000")]
    :expected {:terminal? false
               :texts []}}
   {:test "a-pty-recording-is-not-ssh"
    :name "pty output recorded under an ssh subtype is not ssh"
    ;; Output of a PTY connection, as recorded under the same subtype before
    ;; the hoop SSH proxy.
    :events [[0 "o" (b64 (text-bytes "\u001b[1mroot@host\u001b[0m:~$ "))]
             [1 "i" (b64 (text-bytes "ls\r"))]]
    :expected nil}
   {:test "a-pty-recording-is-not-ssh"
    :name "an empty stream is not ssh"
    :events []
    :expected nil}
   {:test "a-pty-recording-is-not-ssh"
    :name "a stream of gateway errors only is not ssh"
    :events [[0 "e" (b64 (text-bytes "agent offline"))]]
    :expected nil}
   {:test "a-broken-frame-is-not-ssh"
    :name "a broken frame is not ssh"
    :events [(open 0 1 "session")
             [1 "o" (b64 [9 1 0 65])]]
    :expected nil}])

(defn ssh-result
  "The recording as the tests compare it, nil when it is not SSH frames.
  :texts are its events with their bytes decoded."
  [recording]
  (when recording
    {:terminal? (boolean (:terminal? recording))
     :data? (boolean (:data? recording))
     :size [(:width recording) (:height recording)]
     :texts (terminal-decoder/decode-byte-events (:events recording))}))

(defn run-ssh [{:keys [events]}]
  (ssh-result (ssh-decoder/terminal-recording events)))

(defn ssh-matches? [expected result]
  (if (nil? expected)
    (nil? result)
    (and (some? result)
         (= expected (select-keys result (keys expected))))))

;; ─── session-live-tail/live-terminal-events ────────────────────────────────

(def ^:private open-session [0 "i" (frame 1 1 (text-bytes "session") [0])])
(def ^:private open-forward [0 "i" (frame 1 1 (text-bytes "direct-tcpip") [0])])
(def ^:private forward-data [1 "i" (frame 3 1 (text-bytes "GET /"))])

(def live-tail-cases
  [{:test "an-ssh-session-waits-as-a-terminal-before-its-first-data"
    :name "an ssh session with no events waits as a terminal"
    ;; The viewer opened before the shell started.
    :recording-format "ssh"
    :events []
    :finalize? false
    :expected []}
   {:test "an-ssh-session-waits-as-a-terminal-before-its-first-data"
    :name "an ssh session waits as a terminal before its pty-req"
    ;; The channel is open, its pty-req has not arrived yet.
    :recording-format "ssh"
    :events [open-session]
    :finalize? false
    :expected []}
   {:test "a-gateway-error-shows-while-waiting"
    :name "a gateway error shows while waiting"
    :recording-format "ssh"
    :events [open-session [2 "e" (b64 (text-bytes "agent is offline"))]]
    :finalize? true
    :expected [[2 "e" "agent is offline"]]}
   {:test "an-ssh-channel-without-a-pty-renders-rows"
    :name "an ssh channel without a pty renders rows"
    :recording-format "ssh"
    :events [open-forward forward-data]
    :finalize? false
    :expected nil}
   {:test "raw-sessions-render-rows"
    :name "a raw session renders rows"
    :recording-format "raw"
    :events []
    :finalize? false
    :expected nil}])

(defn run-live-tail [{:keys [recording-format events finalize?]}]
  (live-tail/live-terminal-events recording-format events finalize?))

(defn cases-of
  "The cases of one deftest. Throws when there is none, so a renamed test
  fails instead of checking nothing."
  [cases test-name]
  (let [found (filterv #(= test-name (:test %)) cases)]
    (when (empty? found)
      (throw (ex-info (str "no session vector for test " test-name) {:test test-name})))
    found))
