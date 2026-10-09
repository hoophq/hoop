(ns webapp.audit.views.ssh-decoder-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.audit.views.ssh-decoder :as ssh-decoder]
   [webapp.audit.views.terminal-decoder :as terminal-decoder]))

(defn- b64 [bytes]
  (js/btoa (apply str (map #(js/String.fromCharCode %) bytes))))

(defn- text-bytes [s] (map #(.charCodeAt s %) (range (count s))))

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

(defn- data [seconds event-type channel s]
  [seconds event-type (frame 3 channel (text-bytes s))])

(defn- close [seconds channel]
  [seconds "o" (frame 4 channel (text-bytes "session"))])

(defn- texts
  "The recording's events with their bytes decoded, for comparison."
  [recording]
  (terminal-decoder/decode-byte-events (:events recording)))

(deftest interactive-shell-replays-terminal-bytes
  (let [recording (ssh-decoder/terminal-recording
                   [(open 0 1 "session")
                    (pty-req 0.1 1 120 40)
                    (request 0.2 1 "shell" [])
                    [0.3 "o" (frame 5 1 [1])]
                    (data 0.4 "o" 1 "$ ")
                    (data 0.5 "i" 1 "d")
                    (data 0.6 "o" 1 "d")
                    (close 0.7 1)
                    [0.8 "e" (b64 (text-bytes "connection closed"))]])]
    (is (:terminal? recording))
    (is (= [120 40] [(:width recording) (:height recording)]))
    (is (= [[0.4 "o" "$ "] [0.5 "i" "d"] [0.6 "o" "d"] [0.8 "e" "connection closed"]]
           (texts recording)))))

(deftest window-change-resizes-the-terminal
  (let [recording (ssh-decoder/terminal-recording
                   [(open 0 1 "session")
                    (pty-req 0 1 80 24)
                    (request 1 1 "window-change" (concat (uint32 200) (uint32 50)
                                                         (uint32 0) (uint32 0)))
                    (data 2 "o" 1 "x")])]
    (is (= [80 24] [(:width recording) (:height recording)]))
    (is (= [[1 "r" "200x50"] [2 "o" "x"]] (texts recording)))))

(deftest only-pty-channels-reach-the-terminal
  (let [recording (ssh-decoder/terminal-recording
                   [(open 0 1 "session")
                    (pty-req 0 1 80 24)
                    (open 1 2 "direct-tcpip")
                    (data 2 "i" 2 "GET / HTTP/1.1")
                    (open 3 3 "session")
                    (request 3 3 "subsystem" (text-bytes "sftp"))
                    (data 4 "o" 3 "sftp bytes")
                    (data 5 "o" 1 "prompt")])]
    (is (:terminal? recording))
    (is (= [[5 "o" "prompt"]] (texts recording)))))

(deftest a-reused-channel-id-starts-clean
  ;; A second SSH connection of the session reuses channel 1.
  (let [recording (ssh-decoder/terminal-recording
                   [(open 0 1 "session")
                    (pty-req 0 1 80 24)
                    (data 1 "o" 1 "first")
                    (close 2 1)
                    (open 3 1 "direct-tcpip")
                    (data 4 "o" 1 "forwarded")])]
    (is (:terminal? recording))
    (is (= [[1 "o" "first"]] (texts recording)))))

(deftest overlapping-connections-are-not-replayed
  ;; A second connection opens channel 1 while the first one still uses it:
  ;; the stream cannot tell their bytes apart.
  (let [recording (ssh-decoder/terminal-recording
                   [(open 0 1 "session")
                    (pty-req 0 1 80 24)
                    (open 1 1 "session")
                    (request 1 1 "exec" (concat (uint32 3) (text-bytes "scp")))
                    (data 2 "o" 1 "which one?")])]
    (is (some? recording))
    (is (not (:terminal? recording)))))

(deftest concurrent-terminals-are-not-replayed
  ;; One connection with two shells open at once (ssh ControlMaster).
  (let [recording (ssh-decoder/terminal-recording
                   [(open 0 1 "session")
                    (pty-req 0 1 80 24)
                    (open 1 2 "session")
                    (pty-req 1 2 80 24)
                    (data 2 "o" 1 "first")
                    (data 3 "o" 2 "second")])]
    (is (some? recording))
    (is (not (:terminal? recording)))))

(deftest sequential-terminals-replay
  (let [recording (ssh-decoder/terminal-recording
                   [(open 0 1 "session")
                    (pty-req 0 1 80 24)
                    (data 1 "o" 1 "first")
                    (close 2 1)
                    (open 3 2 "session")
                    (pty-req 3 2 80 24)
                    (data 4 "o" 2 "second")])]
    (is (:terminal? recording))
    ;; the second shell's pty-req sets its own size
    (is (= [[1 "o" "first"] [3 "r" "80x24"] [4 "o" "second"]] (texts recording)))))

(deftest a-channel-opened-before-the-stream-is-not-a-terminal
  ;; A live viewer subscribed after the channel was opened: nothing tells
  ;; whether it is a shell, an exec or a port forward.
  (let [recording (ssh-decoder/terminal-recording
                   [(data 0 "o" 1 "$ ")
                    (request 1 1 "window-change" (concat (uint32 120) (uint32 30)
                                                         (uint32 0) (uint32 0)))
                    (data 2 "i" 1 "l")])]
    (is (some? recording))
    (is (not (:terminal? recording)))
    (is (empty? (texts recording)))))

(deftest a-channel-opened-before-the-stream-still-overlaps
  (let [recording (ssh-decoder/terminal-recording
                   [(data 0 "o" 1 "first connection")
                    (open 1 1 "session")
                    (pty-req 1 1 80 24)
                    (data 2 "o" 1 "which one?")])]
    (is (not (:terminal? recording)))))

(deftest a-new-connection-after-an-unopened-channel-closes-replays
  (let [recording (ssh-decoder/terminal-recording
                   [(data 0 "o" 1 "first connection")
                    (close 1 1)
                    (data 2 "i" 1 "late keystroke")
                    (open 3 1 "session")
                    (pty-req 3 1 100 30)
                    (data 4 "o" 1 "second")])]
    (is (:terminal? recording))
    (is (= [100 30] [(:width recording) (:height recording)]))
    (is (= [[4 "o" "second"]] (texts recording)))))

(deftest sessions-without-a-pty-are-not-terminals
  (let [recording (ssh-decoder/terminal-recording
                   [(open 0 1 "session")
                    (request 0 1 "exec" (concat (uint32 15) (text-bytes "git-upload-pack")))
                    (data 1 "o" 1 "0000")])]
    (is (some? recording))
    (is (not (:terminal? recording)))
    (is (empty? (texts recording)))))

(deftest a-pty-recording-is-not-ssh
  ;; Output of a PTY connection, as recorded under the same subtype before
  ;; the hoop SSH proxy.
  (is (nil? (ssh-decoder/terminal-recording
             [[0 "o" (b64 (text-bytes "\u001b[1mroot@host\u001b[0m:~$ "))]
              [1 "i" (b64 (text-bytes "ls\r"))]])))
  (is (nil? (ssh-decoder/terminal-recording [])))
  (is (nil? (ssh-decoder/terminal-recording [[0 "e" (b64 (text-bytes "agent offline"))]]))))

(deftest a-broken-frame-is-not-ssh
  (is (nil? (ssh-decoder/terminal-recording
             [(open 0 1 "session")
              [1 "o" (b64 [9 1 0 65])]]))))
