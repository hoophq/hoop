(ns webapp.audit.views.time-window-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [clojure.string :as cs]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

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
        end-time (r/atom "")
        time-range-error? (r/atom false)]
    (fn []
      [:form  {:class "w-full space-y-radix-7"
               :on-submit (fn [e]
                            (.preventDefault e)
                            (let [start-time @start-time
                                  end-time @end-time]
                              (if (validate-time-range start-time end-time)
                                (on-confirm {:start-time start-time
                                             :end-time end-time})
                                (do
                                  (reset! time-range-error? true)
                                  (js/setTimeout #(reset! time-range-error? false) 5000)))))}
       [:> Box
        [:> Heading {:as "h1" :size "7" :weight "bold" :class "text-gray-12"}
         "Available Time Window"]
        [:> Text {:as "p" :size "3" :class "text-gray-11"}
         "Select the available time window for executing this session's command."]]

       [forms/input {:label "Start Time"
                     :type "time"
                     :required true
                     :value @start-time
                     :on-change #(reset! start-time (-> % .-target .-value))}]

       [forms/input {:label "End Time"
                     :type "time"
                     :required true
                     :value @end-time
                     :on-change #(reset! end-time (-> % .-target .-value))}]

       (when @time-range-error?
         [:> Text {:size "1" :class "text-error-11" :pt "2"}
          "The execution window is invalid. End time must be after start time."])

       [:> Flex {:justify "between" :align "center"}
        [:> Button {:ml "3"
                    :size "3"
                    :variant "ghost"
                    :color "gray"
                    :on-click on-cancel}
         "Cancel"]
        [:> Button {:size "3"
                    :type "submit"}
         "Approve and Set Time Window"]]])))

