(ns webapp.audit.views.ssh-decoder-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.audit.views.session-vectors :as v]))

(defn- check [test-name]
  (doseq [c (v/cases-of v/ssh-cases test-name)]
    (is (v/ssh-matches? (:expected c) (v/run-ssh c)) (:name c))))

(deftest interactive-shell-replays-terminal-bytes
  (check "interactive-shell-replays-terminal-bytes"))

(deftest window-change-resizes-the-terminal
  (check "window-change-resizes-the-terminal"))

(deftest only-pty-channels-reach-the-terminal
  (check "only-pty-channels-reach-the-terminal"))

(deftest a-reused-channel-id-starts-clean
  (check "a-reused-channel-id-starts-clean"))

(deftest overlapping-connections-are-not-replayed
  (check "overlapping-connections-are-not-replayed"))

(deftest concurrent-terminals-are-not-replayed
  (check "concurrent-terminals-are-not-replayed"))

(deftest sequential-terminals-replay
  (check "sequential-terminals-replay"))

(deftest a-channel-opened-before-the-stream-is-not-a-terminal
  (check "a-channel-opened-before-the-stream-is-not-a-terminal"))

(deftest a-channel-opened-before-the-stream-still-overlaps
  (check "a-channel-opened-before-the-stream-still-overlaps"))

(deftest a-new-connection-after-an-unopened-channel-closes-replays
  (check "a-new-connection-after-an-unopened-channel-closes-replays"))

(deftest sessions-without-a-pty-are-not-terminals
  (check "sessions-without-a-pty-are-not-terminals"))

(deftest a-pty-recording-is-not-ssh
  ;; Output of a PTY connection, as recorded under the same subtype before
  ;; the hoop SSH proxy.
  (check "a-pty-recording-is-not-ssh"))

(deftest a-broken-frame-is-not-ssh
  (check "a-broken-frame-is-not-ssh"))
