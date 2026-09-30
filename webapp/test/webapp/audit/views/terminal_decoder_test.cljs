(ns webapp.audit.views.terminal-decoder-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.audit.views.terminal-decoder :as terminal-decoder]))

(defn- b64 [bytes]
  (js/btoa (apply str (map #(js/String.fromCharCode %) bytes))))

(deftest output-character-split-across-events
  (let [events [[0 "o" (b64 [0xE2])]
                [1 "o" (b64 [0x82 0xAC])]]]
    (is (= [[0 "o" ""] [1 "o" "€"]]
           (terminal-decoder/decode-events events)))))

(deftest input-must-not-contaminate-output
  (let [events [[0 "i" (b64 [0xE2])]
                [1 "o" (b64 [0x41])]
                [2 "i" (b64 [0x82 0xAC])]
                [3 "o" (b64 [0xE2])]
                [4 "e" (b64 [0x82 0xAC])]]]
    (is (= [[0 "i" ""] [1 "o" "A"] [2 "i" "€"]
            [3 "o" ""] [4 "e" "€"]]
           (terminal-decoder/decode-events events)))))

(deftest incomplete-final-output-is-visible
  (is (= [[0 "o" "�"]]
         (terminal-decoder/decode-events [[0 "o" (b64 [0xE2])]]))))

(deftest live-output-waits-for-the-rest-of-a-character
  (let [first-event [0 "o" (b64 [0xE2])]
        second-event [1 "o" (b64 [0x82 0xAC])]]
    (is (= [[0 "o" ""]]
           (terminal-decoder/decode-events [first-event] false)))
    (is (= [[0 "o" ""] [1 "o" "€"]]
           (terminal-decoder/decode-events [first-event second-event] false)))))
