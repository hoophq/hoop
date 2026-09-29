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

(deftest persisted-format-overrides-legacy-type
  (is (= "raw" (session-format/recording-format
                 {:type "custom" :connection_subtype "kubernetes"
                  :recording_format "raw"})))
  (is (= "pty" (session-format/recording-format
                 {:type "custom" :recording_format "pty"}))))
