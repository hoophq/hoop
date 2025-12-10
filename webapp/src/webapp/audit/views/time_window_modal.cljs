(ns webapp.audit.views.time-window-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [clojure.string :as cs]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.formatters :as formatters]))

(defn- validate-time-range
  "Validates that end time is after start time"
  [start-time end-time]
  (when (and start-time end-time (> (count start-time) 0) (> (count end-time) 0))
    (let [start-parts (cs/split start-time #":")
          end-parts (cs/split end-time #":")
          start-hours (js/parseInt (first start-parts))
          start-minutes (js/parseInt (second start-parts))
          end-hours (js/parseInt (first end-parts))
          end-minutes (js/parseInt (second end-parts))
          start-total (+ (* start-hours 60) start-minutes)
          end-total (+ (* end-hours 60) end-minutes)]
      (> end-total start-total))))

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
                    :on-click #(when (validate-time-range @start-time @end-time)
                                 (let [start-utc (formatters/local-time->utc-time @start-time)
                                       end-utc (formatters/local-time->utc-time @end-time)]
                                   (when (and start-utc end-utc)
                                     (on-confirm {:start-time start-utc
                                                  :end-time end-utc}))))}
         "Approve and Set Time Window"]]])))

