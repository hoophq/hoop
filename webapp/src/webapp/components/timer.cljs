(ns webapp.components.timer
  (:require [clojure.string :as string]
            [reagent.core :as r]))

(defn insert-0-before [number]
  (-> number
      (#(str "0" %))
      (#(take-last 2 %))
      string/join))

(defn format-time [milliseconds]
  (let [seconds (quot milliseconds 1000)
        minutes (quot seconds 60)
        remaining-seconds (mod seconds 60)]
    (str (insert-0-before minutes) ":" (insert-0-before remaining-seconds))))

(defn decrement-time [time]
  (Math/max 0 (- time 1000)))

;; Original timer (for backward compatibility)
(defn main [create-at access-duration on-timer-end]
  (let [now (.getTime (new js/Date))
        end-date (+ create-at access-duration)
        remaining-time (r/atom (- end-date now))
        interval-id (r/atom nil)]

    (r/create-class
     {:component-did-mount
      (fn [_]
        ;; Start the interval and store its ID for cleanup
        (reset! interval-id
                (js/setInterval #(swap! remaining-time decrement-time) 1000)))

      :component-will-unmount
      (fn [_]
        ;; Clean up the interval to prevent memory leaks
        (when @interval-id
          (js/clearInterval @interval-id)))

      :reagent-render
      (fn []
        (when (<= @remaining-time 0)
          (when @interval-id
            (js/clearInterval @interval-id))
          (on-timer-end))
        [:<>
         [:small {:class (if (<= @remaining-time 60000)
                           "text-red-700"
                           "text-gray-700")}
          "Time left: "]
         [:small {:class (str "font-bold "
                              (if (<= @remaining-time 60000)
                                "text-red-700"
                                "text-gray-700"))}
          (format-time @remaining-time)]])})))

;; New improved timer for database access (uses expire_at directly)
(defn expire-at-timer
  "Timer that counts down to a specific expiration timestamp"
  [expire-at-iso on-timer-end]
  (let [expire-at-ms (.getTime (js/Date. expire-at-iso))
        remaining-time (r/atom (- expire-at-ms (.getTime (js/Date.))))
        interval-id (r/atom nil)]

    (r/create-class
     {:component-did-mount
      (fn [_]
        ;; Start the interval and store its ID for cleanup
        (reset! interval-id
                (js/setInterval
                 #(let [now (.getTime (js/Date.))
                        remaining (- expire-at-ms now)]
                    (reset! remaining-time remaining))
                 1000)))

      :component-will-unmount
      (fn [_]
        ;; Clean up the interval to prevent memory leaks
        (when @interval-id
          (js/clearInterval @interval-id)))

      :reagent-render
      (fn []
        (when (<= @remaining-time 0)
          (when @interval-id
            (js/clearInterval @interval-id))
          (on-timer-end))
        [:<>
         [:small {:class (if (<= @remaining-time 60000)
                           "text-red-700"
                           "text-gray-700")}
          "Time left: "]
         [:small {:class (str "font-bold "
                              (if (<= @remaining-time 60000)
                                "text-red-700"
                                "text-gray-700"))}
          (format-time @remaining-time)]])})))
