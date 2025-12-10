(ns webapp.audit.views.time-window-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [clojure.string :as cs]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn- parse-time-to-date
  "Converts time string (HH:mm format) to Date object in UTC"
  [time-str]
  (when (and time-str (> (count time-str) 0))
    (let [parts (cs/split time-str #":")
          hours (js/parseInt (first parts))
          minutes (js/parseInt (second parts))
          now (js/Date.)
          date (js/Date. (.getUTCFullYear now)
                         (.getUTCMonth now)
                         (.getUTCDate now)
                         hours
                         minutes)]
      date)))

(defn main [{:keys [on-confirm on-cancel]}]
  (let [start-time (r/atom "")
        end-time (r/atom "")]
    (fn [{:keys [on-confirm on-cancel]}]
      [:> Box {:class "w-full"}
       [:> Text {:size "6" :weight "bold" :class "mb-2"}
        "Available Time Window"]
       [:> Text {:size "2" :class "mb-6 text-gray-600"}
        "Select the available time window for executing this session's command."]

       [:> Box {:class "mb-4"}
        [forms/input {:label "Start Time"
                      :type "time"
                      :value @start-time
                      :on-change #(reset! start-time (-> % .-target .-value))}]]

       [:> Box {:class "mb-6"}
        [forms/input {:label "End Time"
                      :type "time"
                      :value @end-time
                      :on-change #(reset! end-time (-> % .-target .-value))}]]

       [:> Flex {:justify "end" :gap "3" :mt "6"}
        [:> Button {:variant "soft"
                    :on-click on-cancel}
         "Cancel"]
        [:> Button {:color "green"
                    :on-click #(let [start (parse-time-to-date @start-time)
                                     end (parse-time-to-date @end-time)]
                                 (when (and start end (> (.getTime end) (.getTime start)))
                                   (on-confirm {:time-window-start start
                                                :time-window-end end})))}
         "Approve and Set Time Window"]]])))

