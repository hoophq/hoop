(ns webapp.audit.views.session-live-tail-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.audit.views.session-vectors :as v]))

(defn- check [test-name]
  (doseq [c (v/cases-of v/live-tail-cases test-name)]
    (is (= (:expected c) (v/run-live-tail c)) (:name c))))

(deftest an-ssh-session-waits-as-a-terminal-before-its-first-data
  (check "an-ssh-session-waits-as-a-terminal-before-its-first-data"))

(deftest a-gateway-error-shows-while-waiting
  (check "a-gateway-error-shows-while-waiting"))

(deftest an-ssh-channel-without-a-pty-renders-rows
  (check "an-ssh-channel-without-a-pty-renders-rows"))

(deftest raw-sessions-render-rows
  (check "raw-sessions-render-rows"))
