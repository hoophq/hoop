(ns webapp.webclient.log-area.logs
  (:require ["@radix-ui/themes" :refer [Box Button Callout Spinner Flex Text]]
            ["lucide-react" :refer [AlertTriangle Clock]]
            [re-frame.core :as rf]
            [webapp.audit.session-details-bridge]
            [webapp.components.log-lines :as log-lines]
            [webapp.components.virtualized-list :as vlist]
            [webapp.formatters :as formatters]))

(defn- clipped-notice
  [clipped menu-has-full-output?]
  [:> Callout.Root {:color "amber"
                    :size "1"
                    :class "mb-3 whitespace-normal flex-shrink-0"}
   [:> Callout.Icon
    [:> AlertTriangle {:size 16}]]
   [:> Callout.Text {:size "1"}
    (str clipped (if (= clipped 1) " line" " lines") " longer than "
         (log-lines/humanize-size log-lines/max-line-chars)
         " clipped for display."
         (when menu-has-full-output?
           " Use the output menu to download or copy the complete result."))]])

(defn- row [line _idx]
  (log-lines/row line ""))

(defn- logs-area-list
  [status {:keys [rows clipped menu-has-full-output?
                  execution-time has-review? session-id]}]
  (case status
    :success (if has-review?
               [:div {:class "group relative py-regular pl-regular pr-large whitespace-pre"
                      :on-click #(rf/dispatch [:session-details/open {:id session-id :verb "exec"}])}
                [:div {:class "text-sm mb-1"}
                 "This task needs approval. Please click here to see the details."]
                [:div {:class "text-gray-11 text-sm"}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]

               [:div {:class "h-full flex flex-col py-regular pl-regular pr-large"}
                (when (pos? clipped)
                  [clipped-notice clipped menu-has-full-output?])
                ;; Rows change on scroll; keep them out of the section's live region.
                ;; min-h keeps rows visible in a short pane: the section scrolls instead.
                [:div {:class "flex-1 min-h-24 text-sm mb-1"
                       :aria-live "off"}
                 [vlist/virtualized-list
                  {:items rows
                   :item-height log-lines/row-height
                   :render-item row}]]
                [:div {:class "text-gray-11 text-sm flex-shrink-0"}
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
                               :on-click #(rf/dispatch [:session-details/open {:id session-id :verb "exec"}])}
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
      :response -> the actual string with the output

   Output is split into rows once per response and virtualized; a row above
   `log-lines/max-line-chars` is clipped on screen. The full string stays in
   the app db for the output menu."
  [_type _config]
  (let [cache (log-lines/new-cache)
        download-disabled? (rf/subscribe [:gateway->sessions-download-disabled?])
        clipboard-disabled? (rf/subscribe [:gateway->clipboard-disabled?])]
    (fn [type config]
      (let [{:keys [rows clipped]} (log-lines/parse cache (:response config))
            aria-label-text (str "Execution output. "
                                 (case (:status config)
                                   :success (str "Status: success. "
                                                 (count rows) " lines"
                                                 (when (pos? clipped)
                                                   (str ", " clipped " clipped for display")))
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
             {:rows rows
              :clipped clipped
              :menu-has-full-output? (not (and @download-disabled? @clipboard-disabled?))
              :execution-time (:execution-time config)
              :has-review? (:has-review config)
              :session-id (:response-id config)}])]]))))
