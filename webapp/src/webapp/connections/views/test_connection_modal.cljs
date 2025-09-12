(ns webapp.connections.views.test-connection-modal
  (:require
   ["@radix-ui/themes" :refer [Box Button Dialog Flex Heading Text]]
   [re-frame.core :as rf]))

(defn get-status-display [status]
  (case status
    :checking [:> Text {:size "2" :class "text-gray-11"} "Checking..."]
    :online [:> Text {:size "2" :class "text-success-11"} "Online"]
    :offline [:> Text {:size "2" :class "text-error-11"} "Offline"]
    :successful [:> Text {:size "2" :class "text-success-11"} "Successful"]
    :failed [:> Text {:size "2" :class "text-error-11"} "Failed"]
    [:> Text {:size "2" :class "text-gray-11"} "Unknown"]))

(defn connectivity-check-content [connection-name test-state]
  (let [loading? (:loading test-state)
        agent-status (:agent-status test-state)
        connection-status (:connection-status test-state)]
    [:> Box {:class "space-y-6"}

     ;; Header
     [:> Flex {:direction "column" :justify "between" :class "space-y-2"}
      [:> Heading {:size "4" :weight "bold"} "Connectivity Check"]
      [:> Text {:size "2" :color "gray"}
       (str "Connection: " connection-name)]
      (when (not loading?)
        [:> Text {:size "1" :color "gray"}
         "Completed in 3.3 seconds"])]

     ;; Details Section
     [:> Box {:class "space-y-4"}
      [:> Heading {:size "3"} "Details"]

      [:> Flex {:direction "column" :justify "between" :class "space-y-2"}
       ;; Agent Status
       [:> Text {:size "2"}
        [:span {:class "font-medium"} "Agent Status: "]
        (get-status-display agent-status)]

       ;; Connection Status
       [:> Text {:size "2"}
        [:span {:class "font-medium"} "Connection Status: "]
        (get-status-display connection-status)]]]]))

(defn test-connection-modal [connection-name]
  (let [test-state @(rf/subscribe [:connections->test-connection])
        open? (and test-state
                   (= (:connection-name test-state) connection-name)
                   (or (:loading test-state)
                       (:agent-status test-state)
                       (:connection-status test-state)))]
    [:> Dialog.Root {:open open?}
     [:> Dialog.Content {:style {:maxWidth "500px"}
                         :class "relative"}
      [:> Box {:class "p-6"}
       [connectivity-check-content connection-name test-state]

       ;; Footer
       [:> Flex {:justify "end" :gap "3" :class "mt-6"}
        [:> Button {:on-click #(rf/dispatch [:connections->close-test-modal])
                    :disabled (:loading test-state)}
         "Done"]]]]]))
