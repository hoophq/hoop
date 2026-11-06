(ns webapp.audit.views.terminal-decoder-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.audit.views.session-vectors :as v]))

(defn- check [test-name]
  (doseq [c (v/cases-of v/terminal-cases test-name)]
    (is (= (:expected c) (v/run-terminal c)) (:name c))))

(deftest output-character-split-across-events
  (check "output-character-split-across-events"))

(deftest input-must-not-contaminate-output
  (check "input-must-not-contaminate-output"))

(deftest incomplete-final-output-is-visible
  (check "incomplete-final-output-is-visible"))

(deftest live-output-waits-for-the-rest-of-a-character
  (check "live-output-waits-for-the-rest-of-a-character"))
