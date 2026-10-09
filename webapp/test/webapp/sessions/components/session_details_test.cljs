(ns webapp.sessions.components.session-details-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.sessions.components.session-details :as session-details]))

(def ^:private denial [{:rule_name "deny-drop" :direction "input"}])

(deftest done-session-with-denial-is-blocked
  (is (= :blocked (session-details/status-key {:status "done" :guardrails_info denial}))))

(deftest done-session-without-denial-keeps-success
  (is (= "done" (session-details/status-key {:status "done"})))
  (is (= "done" (session-details/status-key {:status "done" :guardrails_info []}))))

(deftest other-statuses-ignore-denials
  (is (= "open" (session-details/status-key {:status "open" :guardrails_info denial})))
  (is (= "error" (session-details/status-key {:status "error" :guardrails_info denial}))))
