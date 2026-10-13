(ns webapp.audit.views.session-format-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.audit.views.session-format :as session-format]))

(deftest historical-sessions-keep-their-viewer
  ;; This subtype used to be a command-line PTY. Today's protocol resolver
  ;; calls it an HTTP proxy, but an old recording must still replay as a PTY.
  (is (= "pty" (session-format/recording-format
                 {:type "custom" :connection_subtype "kubernetes"})))
  (is (= "rdp" (session-format/recording-format
                 {:type "custom" :connection_subtype "rdp"})))
  (is (= "pty" (session-format/recording-format {:type "application"}))))

(deftest historical-ssh-sessions-use-the-ssh-viewer
  ;; The ssh viewer replays streams that are not SSH frames as a PTY.
  (doseq [subtype ["ssh" "ssh-local" "git" "github"]]
    (is (= "ssh" (session-format/recording-format
                   {:type "application" :connection_subtype subtype}))))
  (is (= "pty" (session-format/recording-format
                 {:type "custom" :connection_subtype "ssh"}))))

(deftest persisted-format-overrides-legacy-type
  (is (= "raw" (session-format/recording-format
                 {:type "custom" :connection_subtype "kubernetes"
                  :recording_format "raw"})))
  (is (= "pty" (session-format/recording-format
                 {:type "custom" :recording_format "pty"}))))
