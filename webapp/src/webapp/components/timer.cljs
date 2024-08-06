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

(defn main [create-at access-duration on-timer-end]
  (let [now (.getTime (js/Date.))
        end-date (+ create-at access-duration)
        remaining-time (r/atom (- end-date now))]
    (js/setInterval #(swap! remaining-time decrement-time) 1000)
    (r/create-class
     {:component-did-mount (fn [_]
                             (js/setTimeout #(swap! remaining-time decrement-time) (.-getTime (js/Date.)) create-at))
      :reagent-render (fn []
                        (when (<= @remaining-time 0)
                          (on-timer-end))
                        [:div {:class (when (<= @remaining-time 60000) "text-red-700")}
                         [:span
                          "Time left: "]
                         [:span {:class "font-bold"}
                          (format-time @remaining-time)]])})))
