(ns webapp.audit.views.session-details
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout DropdownMenu
                               Flex Text ScrollArea]]
   ["clipboard" :as clipboardjs]
   ["is-url-http" :as is-url-http?]
   ["lucide-react" :refer [Download FileDown Info ChevronDown ArrowUpRight
                           CalendarClock Check CircleCheckBig Clock2 OctagonX CheckCheck]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.audit.views.results-container :as results-container]
   [webapp.audit.views.session-data-raw :as session-data-raw]
   [webapp.audit.views.session-data-video :as session-data-video]
   [webapp.audit.views.data-masking-analytics :as data-masking-analytics]
   [webapp.audit.views.time-window-modal :as time-window-modal]
   [webapp.components.loaders :as loaders]
   [webapp.formatters :as formatters]
   [webapp.routes :as routes]
   [webapp.utilities :as utilities]
   [webapp.sessions.components.session-header :as session-header]
   [webapp.sessions.components.session-details :as session-details-component]))

(def ^:private export-dictionary
  {:postgres "csv"
   :mysql "csv"
   :database "csv"
   :custom "txt"
   :command-line "txt"})

;; TODO: Change it for send DB in the payload and not the response
(defn- sanitize-response [response connection-type]
  (cond
    (= connection-type "mssql")
    (when response
      (if-let [idx (cs/index-of response "\n")]
        (subs response (inc idx))
        response))
    :else response))

(defn- loading-player []
  [:div {:class "flex gap-small items-center justify-center py-large"}
   [:span {:class "italic text-xs text-gray-600"}
    "Loading data for this session"]
   [loaders/simple-loader {:size 4}]])

(defn large-payload-warning [{:keys [session]}]
  [:> Flex {:height "400px"
            :direction "column"
            :gap "5"
            :class "p-[--space-5] bg-[--gray-2] rounded-[9px]"
            :align "center"
            :justify "center"}
   [:> FileDown {:size 48 :color "gray"}]
   [:> Text {:size "3" :class "text-[--gray-11]"}
    "This result is not currently supported to view in browser."]
   [:> Button {:size "3"
               :variant "solid"
               :on-click #(rf/dispatch [:audit->session-file-generate
                                        (:id session)
                                        (get export-dictionary
                                             (keyword (:type session))
                                             "txt")])}
    "Download file"
    [:> Download {:size 18}]]])

(defn large-input-warning [{:keys [session]}]
  [:> Box {:class "w-full p-regular rounded-lg bg-[--gray-2]"}
   [:> Callout.Root {:variant "surface"
                     :size "2"
                     :class "flex items-center mb-small justify-between"}
    [:> Callout.Icon
     [:> Info {:size 16}]]
    [:> Callout.Text {:class "w-full"}
     [:> Flex {:gap "4"
               :class "items-center justify-between"}
      [:> Text
       "Input script is too large to display"]
      [:> Button {:size "2"
                  :variant "soft"
                  :class "flex-shrink-0"
                  :on-click #(rf/dispatch [:audit->session-input-download (:id session)])}
       "Download"
       [:> Download {:size 16}]]]]]])

(defmulti ^:private session-event-stream identity)
(defmethod ^:private session-event-stream "command-line"
  [_ session]
  (let [event-stream (:event_stream session)
        session-id (:id session)
        start-date (:start_date session)]
    [session-data-video/main event-stream session-id start-date]))

(defmethod ^:private session-event-stream "application"
  [_ session]
  (let [event-stream (:event_stream session)
        session-id (:id session)
        start-date (:start_date session)]
    [session-data-video/main event-stream session-id start-date]))

(defmethod ^:private session-event-stream "custom"
  [_ session]
  (let [event-stream (:event_stream session)
        session-id (:id session)
        start-date (:start_date session)]
    [session-data-video/main event-stream session-id start-date]))

(defmethod ^:private session-event-stream :default
  [_ session]
  (let [start-date (:start_date session)
        event-stream (:event_stream session)]
    [session-data-raw/main event-stream start-date]))

(defmulti ^:private review-status-icon identity)
(defmethod ^:private review-status-icon "PENDING" [] "waiting-circle-yellow")
(defmethod ^:private review-status-icon "APPROVED" [] "check-black")
(defmethod ^:private review-status-icon "REJECTED" [] "close-red")

(defmulti ^:private review-status-text identity)
(defmethod ^:private review-status-text "PENDING" [_ group]
  [:> Flex {:gap "1" :align "center"}
   [:> Box
    [:> Clock2 {:size 16 :class "text-gray-11"}]]
   [:> Text {:size "2" :class "text-gray-11"}
    (str "Pending by " (:group group))]])
(defmethod ^:private review-status-text "APPROVED" [_ group]
  [:> Flex {:gap "1" :align "center"}
   [:> Box
    [:> CircleCheckBig {:size 16 :class "text-success-11"}]]
   [:> Text {:size "2" :class "text-success-11"}
    (str
     (when (:forced_review group) "Force ")
     "Approved by " (:group group))]])
(defmethod ^:private review-status-text "REJECTED" [_ group]
  [:> Flex {:gap "1" :align "center"}
   [:> Box
    [:> OctagonX {:size 16 :class "text-error-11"}]]
   [:> Text {:size "2" :class "text-error-11"}
    (str "Rejected by " (:group group))]])

(defn main [session]
  (let [user-details (rf/subscribe [:users->current-user])
        session-details (rf/subscribe [:audit->session-details])
        connection-details (rf/subscribe [:connections->connection-details])
        clipboard-disabled? (rf/subscribe [:gateway->clipboard-disabled?])
        executing-status (r/atom :ready)]
    (rf/dispatch [:gateway->get-info])
    (when session
      (rf/dispatch [:audit->get-session-by-id session]))
    (fn []
      (r/with-let []
        (let [session (:session @session-details)
              user (:data @user-details)
              session-user-id (:user_id session)
              current-user-id (:id user)
              connection-name (:connection session)
              connection-subtype (:connection_subtype session)
              is-session-owner? (= session-user-id current-user-id)
              has-large-payload? (:has-large-payload? @session-details)
              has-large-input? (:has-large-input? @session-details)
              review-status (-> session :review :status)
              review-groups (-> session :review :review_groups_data)
              has-review? (boolean (seq (-> session :review)))
              _ (when (and has-review?
                           connection-name
                           (not (:loading @connection-details))
                           (not= (:name (:data @connection-details)) connection-name))
                  (rf/dispatch [:connections->get-connection-details connection-name]))
              ready? (= (:status session) "ready")
              can-review? (let [user-groups (set (:groups user))]
                            (and (some (fn [review-group]
                                         (and (= "PENDING" (:status review-group))
                                              (contains? user-groups (:group review-group))))
                                       review-groups)
                                 (= "PENDING" review-status)))
              can-force-approve? (let [user-groups (set (:groups user))
                                       connection-data (:data @connection-details)
                                       force-groups (when connection-data
                                                      (set (:force_approve_groups connection-data)))]
                                   (and can-review?
                                        force-groups
                                        (some #(contains? force-groups %) user-groups)))
              handle-reject (fn []
                              (rf/dispatch [:audit->add-review
                                            session
                                            "rejected"]))
              handle-approve (fn []
                               (rf/dispatch [:audit->add-review
                                             session
                                             "approved"]))
              handle-force-approve (fn []
                                     (rf/dispatch [:audit->add-review
                                                   session
                                                   "approved"
                                                   :force-review true]))
              handle-approve-time-window (fn [data]
                                           (rf/dispatch [:audit->add-review
                                                         session
                                                         "approved"
                                                         :start-time (:start-time data)
                                                         :end-time (:end-time data)])
                                           (rf/dispatch [:modal->close]))
              open-time-window-modal (fn []
                                       (rf/dispatch [:modal->open {:id "time-window-modal"
                                                                   :maxWidth "500px"
                                                                   :content [time-window-modal/main
                                                                             {:on-confirm handle-approve-time-window
                                                                              :on-cancel #(rf/dispatch [:modal->close])}]}]))
              script-data (-> session :script :data)
              metadata (-> session :metadata)
              runbook-params (js->clj
                              (js/JSON.parse (-> session :labels :runbookParameters))
                              :keywordize-keys true)]
          [:<>
           ;; New session header component
           [session-header/main {:session session
                                 :on-close #(rf/dispatch [:modal->close])
                                 :clipboard-disabled? @clipboard-disabled?}]

           [:> Box {:class "space-y-radix-5"}

            ;; New session details component
            [session-details-component/main {:session session
                                             :review-groups review-groups
                                             :review-status review-status}]

            ;; runbook params
            (when (and runbook-params
                       (seq runbook-params))
              [:div {:class "flex gap-regular items-center py-small border-b border-t"}
               [:header {:class "px-small text-sm font-bold"}
                "Parameters"]
               [:section {:class "flex items-center gap-regular flex-grow text-xs border-l p-regular"}
                (doall
                 (for [[param-key param-value] runbook-params]
                   ^{:key param-key}
                   [:div
                    [:span {:class "font-bold text-gray-500"}
                     param-key ": "]
                    [:span param-value]]))]])
            ;; end runbook params

            ;; metadata
            (when (and metadata
                       (seq metadata))
              [:div
               (doall
                (for [[metadata-key metadata-value] metadata]
                  ^{:key metadata-key}
                  [:div {:class "flex gap-small items-center py-small border-t last:border-b"}
                   [:header {:class "w-32 px-small text-sm font-bold"}
                    metadata-key]
                   [:section {:class "w-full text-xs border-l p-small"}
                    (if (is-url-http? metadata-value)
                      [:a {:href metadata-value
                           :target "_blank"
                           :class "text-blue-600 underline"}
                       metadata-value]
                      [:span metadata-value])]]))])
            ;; end metadata

            ;; script area
            (when (or script-data has-large-input?)
              [:section {:id "session-script"}
               (if has-large-input?
                 [large-input-warning {:session session}]
                 (when (and script-data (> (count script-data) 0))
                   [:> ScrollArea {:style {:maxHeight "160px"}}
                    [:div
                     {:class (str "w-full p-regular whitespace-pre "
                                  "rounded-lg bg-gray-100 "
                                  "text-xs text-gray-800 font-mono")}
                     [:article script-data]]]))])
            ;; end script area


            [data-masking-analytics/main {:session session}]

            ;; response logs area
            (when-not (or ready?
                          (and (some #(= "PENDING" (:status %))
                                     review-groups)
                               (= "PENDING" review-status)))
              [:section {:id "session-event-stream"}
               (if (= (:status @session-details) :loading)
                 [loading-player]

                 [:<>
                  (if has-large-payload?
                    [large-payload-warning
                     {:session session}]

                    [:div {:class "h-full"}
                     (if (= (:verb session) "exec")
                       [results-container/main
                        connection-subtype
                        {:results (sanitize-response
                                   (utilities/decode-b64 (or (first (:event_stream session)) ""))
                                   connection-subtype)
                         :results-status (:status @session-details)
                         :fixed-height? true
                         :results-id (:id session)}]
                       [session-event-stream (:type session) session])])])])

            ;; action buttons section
            (when can-review?
              [:> Flex {:justify "end" :gap "2"}
               ;; Time window message when pending approvals remain
               (when (and (not ready?)
                          (= (:verb session) "exec")
                          (get-in session [:review :time_window :configuration :start_time])
                          (get-in session [:review :time_window :configuration :end_time]))
                 (let [start-time-utc (get-in session [:review :time_window :configuration :start_time])
                       end-time-utc (get-in session [:review :time_window :configuration :end_time])
                       start-time (formatters/utc-time->display-time start-time-utc)
                       end-time (formatters/utc-time->display-time end-time-utc)]
                   [:> Flex {:align "center" :justify "end" :gap "2"}
                    [:> CalendarClock {:size 16 :class "text-gray-11"}]
                    [:> Text {:size "2" :class "text-gray-11"}
                     "This session is set to be executed from "
                     [:> Text {:size "2" :weight "medium" :class "text-gray-11"}
                      start-time]
                     " to "
                     [:> Text {:size "2" :weight "medium" :class "text-gray-11"}
                      end-time]
                     "."]]))

               [:> Button {:color "red" :size "2" :variant "soft" :on-click handle-reject}
                "Reject"]

               ;; Approve dropdown
               [:> DropdownMenu.Root
                [:> DropdownMenu.Trigger
                 [:> Button {:size "2" :color "green"}
                  "Approve"
                  [:> ChevronDown {:size 16}]]]
                [:> DropdownMenu.Content
                 (when-not
                  (and (get-in session [:review :time_window :configuration :start_time])
                       (get-in session [:review :time_window :configuration :end_time]))
                   [:> DropdownMenu.Item {:class "flex justify-between gap-4 group"
                                          :on-click open-time-window-modal}
                    "Approve in a Time Window"
                    [:> CalendarClock {:size 16 :class "text-gray-10 group-hover:text-white"}]])
                 [:> DropdownMenu.Item {:class "flex justify-between gap-4 group"
                                        :on-click handle-approve}
                  "Approve"
                  [:> Check {:size 16 :class "text-gray-10 group-hover:text-white"}]]
                 (when can-force-approve?
                   [:<>
                    [:> DropdownMenu.Separator]
                    [:> DropdownMenu.Item {:class "flex justify-between gap-4 group"
                                           :on-click handle-force-approve}
                     "Force Approve"
                     [:> CheckCheck {:size 16 :class "text-gray-10 group-hover:text-white"}]]])]]])

            ;; execution schedule information (time window)
            (when (and ready?
                       (= (:verb session) "exec")
                       (get-in session [:review :time_window :configuration :start_time])
                       (get-in session [:review :time_window :configuration :end_time]))
              (let [start-time-utc (get-in session [:review :time_window :configuration :start_time])
                    end-time-utc (get-in session [:review :time_window :configuration :end_time])
                    start-time (formatters/utc-time->display-time start-time-utc)
                    end-time (formatters/utc-time->display-time end-time-utc)
                    within-window? (formatters/is-within-time-window? start-time-utc end-time-utc)]
                [:> Flex {:align "center" :justify "end" :gap "4"}
                 [:> Text {:size "2" :class "text-gray-11"}
                  "This session is ready and available to be executed from "
                  [:> Text {:size "2" :weight "medium" :class "text-gray-11"}
                   start-time]
                  " to "
                  [:> Text {:size "2" :weight "medium" :class "text-gray-11"}
                   end-time]
                  "."]
                 (when is-session-owner?
                   [:> Flex {:justify "end" :gap "2"}
                    [:> Button {:loading (when (= @executing-status :loading)
                                           true)
                                :disabled (not within-window?)
                                :on-click (fn []
                                            (reset! executing-status :loading)
                                            (rf/dispatch [:audit->execute-session session]))}
                     "Execute"]])]))

            ;; Execute button (when no time window is configured)
            (when (and ready?
                       (= (:verb session) "exec")
                       is-session-owner?
                       (not (get-in session [:review :time_window :configuration :start_time])))
              [:> Flex {:align "center" :justify "end" :gap "4"}
               [:> Text {:size "2" :class "text-gray-11"}
                "This session is ready to be executed."]

               [:> Button {:loading (when (= @executing-status :loading)
                                      true)
                           :on-click (fn []
                                       (reset! executing-status :loading)
                                       (rf/dispatch [:audit->execute-session session]))}
                "Execute"]])]])

        (finally
          (rf/dispatch [:audit->clear-session])
          (rf/dispatch [:reports->clear-session-report-by-id]))))))

