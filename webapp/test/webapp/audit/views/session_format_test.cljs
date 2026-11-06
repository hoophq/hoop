(ns webapp.audit.views.session-format-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.audit.views.session-vectors :as v]))

(defn- check [test-name]
  (doseq [c (v/cases-of v/format-cases test-name)]
    (is (= (:expected c) (v/run-format c)) (:name c))))

(deftest historical-sessions-keep-their-viewer
  ;; This subtype used to be a command-line PTY. Today's protocol resolver
  ;; calls it an HTTP proxy, but an old recording must still replay as a PTY.
  (check "historical-sessions-keep-their-viewer"))

(deftest historical-ssh-sessions-use-the-ssh-viewer
  ;; The ssh viewer replays streams that are not SSH frames as a PTY.
  (check "historical-ssh-sessions-use-the-ssh-viewer"))

(deftest persisted-format-overrides-legacy-type
  (check "persisted-format-overrides-legacy-type"))
