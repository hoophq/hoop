(ns webapp.webclient.log-area.logs
  (:require ["@radix-ui/themes" :refer [Box Button Spinner Flex Text]]
            ["lucide-react" :refer [Clock]]
            [re-frame.core :as rf]
            [webapp.audit.views.session-details :as session-details]
            [webapp.formatters :as formatters]))

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
    :running [:> Box {:class "group relative py-regular pl-regular pr-large"}
              [:> Flex {:align "start" :gap "3"}
               [:> Box {:class "flex-shrink-0 text-info-11 mt-0.5"}
                [:> Clock {:size 18}]]
               [:> Flex {:direction "column" :gap "2"}
                [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                 "Session is still running"]
                [:> Text {:size "2" :class "text-gray-11"}
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
                   [:> Text {:size "1" :class "text-gray-10 font-mono"}
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
          :session-id (:response-id config)}])]]))
