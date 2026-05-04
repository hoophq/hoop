(ns webapp.webclient.log-area.logs
  (:require ["@radix-ui/themes" :refer [Box Button Spinner Flex Text DropdownMenu]]
            ["lucide-react" :refer [SquareArrowOutUpRight EllipsisVertical Copy Clock]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [webapp.audit.views.session-details :as session-details]
            [webapp.formatters :as formatters]))

(defn action-buttons-container [session-id logs-content]
  (let [clipboard-disabled? (rf/subscribe [:gateway->clipboard-disabled?])]
    [:div {:class "sticky top-1 right-0 h-0 w-full z-30"
           :style {:pointer-events "none"}}
     [:div {:class "absolute -top-10 -right-2"
            :style {:pointer-events "auto"}}
      [:> DropdownMenu.Root
       [:> DropdownMenu.Trigger {:class (str "cursor-pointer p-1.5 rounded-full "
                                             "bg-gray-3 hover:bg-gray-5 shadow-sm "
                                             "opacity-100 transition border border-gray-5")
                                 :aria-label "Output actions menu"}
        [:> Box
         [:> EllipsisVertical {:size 18 :class "text-gray-12" :aria-hidden "true"}]]]
       [:> DropdownMenu.Content
        [:> DropdownMenu.Item {:on-select #(rf/dispatch
                                            [:modal->open
                                             {:id "session-details"
                                              :maxWidth "95vw"
                                              :content [session-details/main {:id session-id :verb "exec"}]}])}
         [:> Flex {:align "center" :gap "2"}
          [:> SquareArrowOutUpRight {:size 16}]
          [:> Text {:size "2"} "View session details"]]]
        (when-not @clipboard-disabled?
          [:> DropdownMenu.Item {:on-select #(js/navigator.clipboard.writeText logs-content)}
           [:> Flex {:align "center" :gap "2"}
            [:> Copy {:size 16}]
            [:> Text {:size "2"} "Copy logs content"]]])]]]]))

(defn- logs-area-list
  [status {:keys [logs logs-status execution-time has-review? session-id]}]
  (case status
    :success (if has-review?
               [:div {:class "group relative py-regular pl-regular pr-large whitespace-pre"
                      :on-click (fn []
                                  (rf/dispatch (rf/dispatch
                                                [:modal->open
                                                 {:id "session-details"
                                                  :maxWidth "95vw"
                                                  :content [session-details/main {:id session-id :verb "exec"}]}])))}
                [:div {:class "text-sm mb-1"}
                 "This task needs to be reviewed. Please click here to see the details."]
                [:div {:class "text-gray-11 text-sm"}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]

               [:div {:class " group relative py-regular pl-regular pr-large whitespace-pre"}
                [:div {:class "text-sm mb-1"}
                 logs]
                [:div {:class (str (if (= logs-status "success")
                                     "text-gray-11 text-sm"
                                     "text-gray-11 text-sm"))}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]])
    :loading [:div {:class "flex gap-regular py-regular pl-regular pr-large"}
              [:> Spinner {:loading true}]
              [:span "loading"]]
    :running [:div {:class "group relative py-regular pl-regular pr-large"}
              [:div {:class "flex items-start gap-3"}
               [:div {:class "flex-shrink-0 text-info-11 mt-0.5"}
                [:> Clock {:size 18}]]
               [:div {:class "flex flex-col gap-2"}
                [:div {:class "text-sm font-medium text-gray-12"}
                 "Session is still running"]
                [:div {:class "text-sm text-gray-11"}
                 (str "The gateway timed out after 50s waiting for the result. "
                      "Your session keeps executing in the background.")]
                (when session-id
                  [:<>
                   [:> Button {:size "1"
                               :variant "soft"
                               :on-click (fn []
                                           (rf/dispatch
                                            [:modal->open
                                             {:id "session-details"
                                              :maxWidth "95vw"
                                              :content [session-details/main {:id session-id :verb "exec"}]}]))}
                    "View session details"]
                   [:div {:class "text-gray-10 text-xs font-mono"}
                    (str "Session: " session-id)]])]]]
    :failure [:div {:class " group relative py-regular pl-regular pr-large whitespace-pre"}
              [:div {:class "text-sm mb-1"}
               "There was an error to get the logs for this task"]
              [:div {:class "text-gray-11 text-sm"}
               (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]
    [:div {:class "flex gap-regular py-regular pl-regular pr-large"}
     [:span  "No logs to show"]]))

(defn main
  "config is a map with the following fields:
      :status -> possible values are :success :running :loading :failure. Anything different will be default to an generic error message
      :id -> id to differentiate more than one log on the same page.
      :logs -> the actual string with the logs"
  [type config]
  (let [line-count (when (:response config)
                     (count (clojure.string/split-lines (:response config))))
        aria-label-text (str "Execution output. "
                             (case (:status config)
                               :success (str "Status: success. " line-count " lines")
                               :running "Status: still running after gateway timeout"
                               :loading "Status: executing..."
                               :failure "Status: failed"
                               "No output"))]
    [:div {:class "relative h-full"}
     [:section
      {:class (str "bg-gray-2 font-mono h-full"
                   " whitespace-pre text-gray-11 text-sm overflow-auto"
                   " h-full")
       :role "log"
       :tabIndex "0"
       :aria-label aria-label-text
       :aria-live (if (= (:status config) :loading) "assertive" "polite")
       :style {:overflow-anchor "none"}}
      (case type
        :logs
        [logs-area-list (:status config)
         {:logs (:response config)
          :logs-status (:response-status config)
          :script (:script config)
          :execution-time (:execution-time config)
          :has-review? (:has-review config)
          :session-id (:response-id config)}])]

     (case (:status config)
       :success
       (if (:has-review config)
         [:div {:class "absolute top-1 right-4 z-30 pointer-events-none"}
          [:div {:class "pointer-events-auto"}
           [action-buttons-container (:response-id config) "This task needs to be reviewed. Please click here to see the details."]]]

         [:div {:class "absolute top-1 right-4 z-30 pointer-events-none"}
          [:div {:class "pointer-events-auto"}
           [action-buttons-container (:response-id config) (:response config)]]])
       :running
       (when (:response-id config)
         [:div {:class "absolute top-1 right-4 z-30 pointer-events-none"}
          [:div {:class "pointer-events-auto"}
           [action-buttons-container (:response-id config) ""]]])
       :failure
       [:div {:class "absolute top-1 right-4 z-30 pointer-events-none"}
        [:div {:class "pointer-events-auto"}
         [action-buttons-container (:response-id config) "There was an error to get the logs for this task"]]]

       nil)]))
