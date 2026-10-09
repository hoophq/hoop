(ns webapp.audit.views.session-live-tail-test
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.audit.views.session-live-tail :as live-tail]))

(defn- b64 [bytes]
  (js/btoa (apply str (map #(js/String.fromCharCode %) bytes))))

(defn- text-bytes [s] (map #(.charCodeAt s %) (range (count s))))

(defn- frame [frame-type channel & parts]
  (b64 (concat [frame-type channel 0] (apply concat parts))))

(def ^:private open-session [0 "i" (frame 1 1 (text-bytes "session") [0])])
(def ^:private open-forward [0 "i" (frame 1 1 (text-bytes "direct-tcpip") [0])])
(def ^:private forward-data [1 "i" (frame 3 1 (text-bytes "GET /"))])

(deftest an-ssh-session-waits-as-a-terminal-before-its-first-data
  ;; The viewer opened before the shell started.
  (is (= [] (live-tail/live-terminal-events "ssh" [] false)))
  ;; The channel is open, its pty-req has not arrived yet.
  (is (= [] (live-tail/live-terminal-events "ssh" [open-session] false))))

(deftest a-gateway-error-shows-while-waiting
  (is (= [[2 "e" "agent is offline"]]
         (live-tail/live-terminal-events
          "ssh" [open-session [2 "e" (b64 (text-bytes "agent is offline"))]] true))))

(deftest an-ssh-channel-without-a-pty-renders-rows
  (is (nil? (live-tail/live-terminal-events "ssh" [open-forward forward-data] false))))

(deftest raw-sessions-render-rows
  (is (nil? (live-tail/live-terminal-events "raw" [] false))))
