(ns webapp.components.timer
  (:require [clojure.string :as str]
            [reagent.core :as r]))

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
  "Format milliseconds as HH:MM:SS or MM:SS depending on duration"
  [ms]
  (let [total-seconds (quot ms 1000)
        hours (quot total-seconds 3600)
        minutes (mod (quot total-seconds 60) 60)
        seconds (mod total-seconds 60)]
    (if (> hours 0)
      ;; Format with hours: HH:MM:SS
      (str (pad-zero hours) ":" (pad-zero minutes) ":" (pad-zero seconds))
      ;; Format without hours: MM:SS
      (str (pad-zero minutes) ":" (pad-zero seconds)))))


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

(defn inline-timer
  "Inline timer for use within text"
  [{:keys [expire-at on-complete text-component]}]

  (let [expire-ms (.getTime (js/Date. expire-at))
        remaining-ms (use-countdown expire-ms on-complete)]

    (text-component (format-duration remaining-ms))))
