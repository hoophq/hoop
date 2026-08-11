(ns webapp.webclient.log-area.logs
  (:require ["@radix-ui/themes" :refer [Box Button Callout Spinner Flex Text]]
            ["lucide-react" :refer [AlertTriangle Clock]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [webapp.audit.views.session-details :as session-details]
            [webapp.formatters :as formatters]))

(def ^:const max-rendered-chars
  "Upper bound on how much output reaches the DOM.

  This panel renders output into one unvirtualized `whitespace-pre` node, and
  Chrome clamps layout at 2^25 px — which a single unwrapped line hits at ~3.8M
  characters, leaving the viewport blank behind a huge scrollbar (EVL-121).
  Measured at 14px monospace: 1MB lays out in 29ms at 8.8M px, ~4x below the
  clamp, where 4MB is already clamped. Full output stays reachable through the
  output menu."
  (* 1024 1024))

(defn- humanize-size
  "Formats a character count as a size; terminal output is effectively ASCII."
  [n]
  (cond
    (>= n (* 1024 1024)) (str (.toFixed (/ n 1024 1024) 1) " MB")
    (>= n 1024) (str (.toFixed (/ n 1024) 1) " KB")
    :else (str n " B")))

(defn- truncation-notice
  [total-chars]
  [:> Callout.Root {:color "amber"
                    :size "1"
                    :class "mb-3 max-w-3xl whitespace-normal"}
   [:> Callout.Icon
    [:> AlertTriangle {:size 16}]]
   [:> Callout.Text {:size "1"}
    (str "Output truncated for display — showing the first "
         (humanize-size max-rendered-chars) " of " (humanize-size total-chars)
         ". Use the output menu to download or view the complete result.")]])

(defn- logs-area-list
  [status {:keys [logs logs-status logs-truncated? logs-total-chars
                  execution-time has-review? session-id]}]
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
                (when logs-truncated?
                  [truncation-notice logs-total-chars])
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
      :logs -> the actual string with the logs

   Output above `max-rendered-chars` is cut before it reaches the DOM; the full
   string stays in the app db for the output menu."
  [type config]
  (let [full-response (:response config)
        total-chars (count full-response)
        truncated? (> total-chars max-rendered-chars)
        ;; Derived from the bounded string, so no render walks the full payload.
        display-response (if truncated?
                           (subs full-response 0 max-rendered-chars)
                           full-response)
        line-count (when display-response
                     (count (cs/split-lines display-response)))
        aria-label-text (str "Execution output. "
                             (case (:status config)
                               :success (str "Status: success. "
                                             line-count " lines"
                                             (when truncated?
                                               (str ", truncated for display out of "
                                                    (humanize-size total-chars)
                                                    " total")))
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
         {:logs display-response
          :logs-truncated? truncated?
          :logs-total-chars total-chars
          :logs-status (:response-status config)
          :script (:script config)
          :execution-time (:execution-time config)
          :has-review? (:has-review config)
          :session-id (:response-id config)}])]]))
