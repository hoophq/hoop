(ns webapp.audit.views.time-window-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn main [{:keys [on-confirm on-cancel]}]
  (let [start-time (r/atom "")
        end-time (r/atom "")]
    (fn []
      [:form  {:class "w-full space-y-radix-7"
               :on-submit (fn [e]
                            (.preventDefault e)
                            (let [start-time @start-time
                                  end-time @end-time]
                              (on-confirm {:start-time start-time
                                           :end-time end-time})))}
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

       [:> Box
        [forms/input {:label "End Time"
                      :type "time"
                      :required true
                      :value @end-time
                      :on-change #(reset! end-time (-> % .-target .-value))}]

        [:> Text {:as "p" :size "2" :class "text-gray-11"}
         "If the end time is earlier than the start time, the time window continues to the next day."]]


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

