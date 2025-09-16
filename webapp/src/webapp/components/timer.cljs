(ns webapp.components.timer
  (:require [clojure.string :as str]
            [reagent.core :as r]
            ["@radix-ui/themes" :refer [Text]]))

;; Helper functions (pure)
(defn- pad-zero
  "Add leading zero to numbers < 10"
  [number]
  (->> number
       str
       (str "0")
       (take-last 2)
       str/join))

(defn- format-duration
  "Format milliseconds as MM:SS"
  [ms]
  (let [total-seconds (quot ms 1000)
        minutes (quot total-seconds 60)
        seconds (mod total-seconds 60)]
    (str (pad-zero minutes) ":" (pad-zero seconds))))


;; Hook for timer logic
(defn- use-countdown
  "Hook that manages countdown state and cleanup"
  [end-timestamp-ms on-complete]
  (r/with-let [remaining-time (r/atom (- end-timestamp-ms (.getTime (js/Date.))))
               update-timer #(let [now (.getTime (js/Date.))
                                   remaining (max 0 (- end-timestamp-ms now))]
                               (reset! remaining-time remaining)
                               (when (and (<= remaining 0) on-complete)
                                 (on-complete)))
               interval-id (js/setInterval update-timer 1000)]

    ;; Initial update
    (update-timer)

    ;; Return current remaining time
    @remaining-time

    ;; Cleanup on unmount
    (finally
      (js/clearInterval interval-id))))

;; Modern functional timer components
(defn countdown-timer
  "Simple countdown timer that shows time remaining until expiration"
  [{:keys [expire-at on-complete urgent-threshold]
    :or {urgent-threshold 60000}}] ; Default 1 minute threshold

  (let [expire-ms (.getTime (js/Date. expire-at))
        remaining-ms (use-countdown expire-ms on-complete)
        is-urgent? (<= remaining-ms urgent-threshold)]

    [:div {:class "flex items-baseline gap-1"}
     [:small {:class (if is-urgent? "text-red-700" "text-gray-700")}
      "Time left: "]
     [:small {:class (str "font-bold " (if is-urgent? "text-red-700" "text-gray-700"))}
      (format-duration remaining-ms)]]))

(defn inline-timer
  "Inline timer for use within text"
  [{:keys [expire-at on-complete urgent-threshold]
    :or {urgent-threshold 60000}}]

  (let [expire-ms (.getTime (js/Date. expire-at))
        remaining-ms (use-countdown expire-ms on-complete)
        is-urgent? (<= remaining-ms urgent-threshold)]

    [:> Text {:size "3" :weight "bold" :class (if is-urgent? "text-[--red-11]" "text-[--gray-11]")}
     (format-duration remaining-ms)]))

(defn session-timer
  "Timer specifically for database access sessions"
  [{:keys [expire-at on-session-end]}]

  [countdown-timer
   {:expire-at expire-at
    :on-complete on-session-end
    :urgent-threshold 60000}]) ; 1 minute warning

(defn main
  "Legacy timer - use countdown-timer instead"
  [created-at-ms duration-ms on-timer-end]
  (let [expire-at (js/Date. (+ created-at-ms duration-ms))]
    [countdown-timer
     {:expire-at expire-at
      :on-complete on-timer-end}]))
