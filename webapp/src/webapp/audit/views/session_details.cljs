(ns webapp.audit.views.session-details
  (:require
   ["@heroicons/react/24/outline" :as hero-outline-icon]
   ["@radix-ui/themes" :refer [Box Button Callout DropdownMenu
                               Flex Text Tooltip ScrollArea]]
   ["clipboard" :as clipboardjs]
   ["is-url-http" :as is-url-http?]
   ["lucide-react" :refer [Download FileDown Info ChevronDown ArrowUpRight
                           CalendarClock Check CircleCheckBig Clock2 OctagonX]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.audit.views.results-container :as results-container]
   [webapp.audit.views.session-data-raw :as session-data-raw]
   [webapp.audit.views.session-data-video :as session-data-video]
   [webapp.audit.views.data-masking-analytics :as data-masking-analytics]
   [webapp.audit.views.time-window-modal :as time-window-modal]
   [webapp.components.button :as button]
   [webapp.components.headings :as h]
   [webapp.components.loaders :as loaders]
   [webapp.components.user-icon :as user-icon]
   [webapp.config :as config]
   [webapp.formatters :as formatters]
   [webapp.routes :as routes]
   [webapp.utilities :as utilities]))

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

;; indentifies if a session is a runbook or not and re execute it
(defn- re-run-session [session]
  (if (-> session :labels :runbookFile)
    (do
      (let [labels (:labels session)
            file-name (:runbookFile labels)
            params (js/JSON.parse (:runbookParameters labels))
            connection-name (:connection session)
            repository (:runbookRepository labels)
            on-success (fn [res]
                         (rf/dispatch [:audit->get-session-by-id {:id (:session_id res) :verb "exec"}])
                         (rf/dispatch [:audit->get-sessions]))
            on-failure (fn [_error-message error]
                         (rf/dispatch [:audit->get-session-by-id {:id (:session_id error) :verb "exec"}])
                         (rf/dispatch [:audit->get-sessions]))]
        (rf/dispatch [:runbooks/exec {:file-name file-name
                                      :params params
                                      :connection-name connection-name
                                      :repository repository
                                      :on-success on-success
                                      :on-failure on-failure}]))
      (rf/dispatch [:audit->clear-session-details-state {:status :loading}]))
    (do
      (rf/dispatch [:jira-integration->get])
      (rf/dispatch [:audit->re-run-session session]))))

(defmulti ^:private review-status-icon identity)
(defmethod ^:private review-status-icon "PENDING" [] "waiting-circle-yellow")
(defmethod ^:private review-status-icon "APPROVED" [] "check-black")
(defmethod ^:private review-status-icon "REJECTED" [] "close-red")

(defmulti ^:private review-status-text identity)
(defmethod ^:private review-status-text "PENDING" [_ group-name]
  [:> Flex {:gap "1" :align "center"}
   [:> Clock2 {:size 16 :class "text-gray-11"}]
   [:> Text {:size "2" :class "text-gray-11"}
    (str "Pending by " group-name)]])
(defmethod ^:private review-status-text "APPROVED" [_ group-name]
  [:> Flex {:gap "1" :align "center"}
   [:> CircleCheckBig {:size 16 :class "text-success-11"}]
   [:> Text {:size "2" :class "text-success-11"}
    (str "Approved by " group-name)]])
(defmethod ^:private review-status-text "REJECTED" [_ group-name]
  [:> Flex {:gap "1" :align "center"}
   [:> OctagonX {:size 16 :class "text-error-11"}]
   [:> Text {:size "2" :class "text-error-11"}
    (str "Rejected by " group-name)]])

(defn main [session]
  (let [user-details (rf/subscribe [:users->current-user])
        session-details (rf/subscribe [:audit->session-details])
        gateway-info (rf/subscribe [:gateway->info])
        executing-status (r/atom :ready)
        connecting-status (r/atom :ready)
        killing-status (r/atom :ready)]
    (rf/dispatch [:gateway->get-info])
    (when session
      (rf/dispatch [:audit->get-session-by-id session]))
    (fn []
      (r/with-let [clipboard-url (new clipboardjs ".copy-to-clipboard-url")
                   _ (.on clipboard-url "success" #(rf/dispatch [:show-snackbar {:level :success :text "URL copied to clipboard"}]))]
        (let [session (:session @session-details)
              user (:data @user-details)
              session-user-name (:user_name session)
              session-user-id (:user_id session)
              current-user-id (:id user)
              connection-name (:connection session)
              connection-subtype (:connection_subtype session)
              start-date (:start_date session)
              end-date (:end_date session)
              session-batch-id (:session_batch_id session)
              verb (:verb session)
              session-status (:status session)
              has-large-payload? (:has-large-payload? @session-details)
              has-large-input? (:has-large-input? @session-details)
              disabled-download (-> @gateway-info :data :disable_sessions_download)
              review-groups (-> session :review :review_groups_data)
              in-progress? (or (= end-date nil)
                               (= end-date ""))
              all-groups-pending? (every? #(= (:status %) "PENDING") review-groups)
              has-review? (boolean (seq (-> session :review)))
              review-status (when has-review?
                              (some #(when (= (:status %) "APPROVED") "APPROVED") review-groups))
              can-kill-session? (and (= session-status "open")
                                     (or (not has-review?)
                                         (= review-status "APPROVED")))
              ready? (= (:status session) "ready")
              revoke-at (when (get-in session [:review :revoke_at])
                          (js/Date. (get-in session [:review :revoke_at])))
              not-revoked? (when revoke-at (> (.getTime revoke-at) (.getTime (js/Date.))))
              can-connect? (and ready? (= verb "connect") not-revoked?)
              can-review? (let [user-groups (set (:groups user))]
                            (some (fn [review-group]
                                    (and (= "PENDING" (:status review-group))
                                         (contains? user-groups (:group review-group))))
                                  review-groups))
              is-session-owner? (= session-user-id current-user-id)
              handle-reject (fn []
                              (rf/dispatch [:audit->add-review
                                            session
                                            "rejected"]))
              handle-approve (fn []
                               (rf/dispatch [:audit->add-review
                                             session
                                             "approved"]))
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
                              :keywordize-keys true)
              kill-session (fn []
                             (reset! killing-status :loading)
                             (rf/dispatch [:audit->kill-session session killing-status]))]
          [:> Box {:class "space-y-radix-5"}
           [:header {:class "mr-large"}
            [:div {:class "flex"}
             [:div {:class "flex flex-col lg:flex-row flex-grow gap-small lg:items-baseline"}
              [:div {:class "flex flex-col"}
               [h/h2 connection-name]
               [:div {:class "text-xs flex flex-grow gap-1"}
                [:span {:class "text-gray-500"}
                 "type:"]
                [:span {:class "text-xs font-bold"}
                 (:type session)]]]

              (when (and in-progress? (not ready?))
                [:div {:class "flex gap-small lg:justify-end items-center h-full lg:ml-large"}
                 [:div {:class "rounded-full w-1.5 h-1.5 bg-green-500"}]
                 [:span {:class "text-xs text-gray-500"}
                  "This session has pending items"]])

              (when can-connect?
                [:div {:class "flex gap-regular justify-end items-center mx-large"}
                 [:span {:class "text-xs text-gray-500"}
                  "This session is ready to be connected"]
                 [button/primary {:text "Connect"
                                  :status @connecting-status
                                  :on-click (fn []
                                              (reset! connecting-status :loading)
                                              (rf/dispatch [:close-modal])
                                              (rf/dispatch [:audit->connect-session session connecting-status]))
                                  :variant :small}]])]

             [:div {:class "relative flex gap-2.5 items-start pr-3"}
              (when can-kill-session?
                [:div {:class "relative group"}
                 [:> Tooltip {:content "Kill Session"}
                  [:div {:class "rounded-full p-2 bg-red-100 hover:bg-red-200 transition cursor-pointer"
                         :on-click kill-session}
                   (if (= @killing-status :loading)
                     [loaders/simple-loader {:size 2}]
                     [:> hero-outline-icon/StopIcon {:class "h-5 w-5 text-red-600"}])]]])

              (when (and (= (:verb session) "exec")
                         (nil? (-> session :integrations_metadata :jira_issue_url)))
                [:div {:class "relative group"}
                 [:> Tooltip {:content "Re-run session"}
                  [:div {:class "rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer"
                         :on-click #(re-run-session session)}
                   [:> hero-outline-icon/PlayIcon {:class "h-5 w-5 text-gray-600"}]]]])

              (when (-> session :integrations_metadata :jira_issue_url)
                [:div {:class "relative group"}
                 [:> Tooltip {:content "Open in Jira"}
                  [:div {:class "rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer"
                         :on-click (fn []
                                     (js/open (-> session :integrations_metadata :jira_issue_url) "_blank"))}
                   [:div
                    [:figure {:class "flex-shrink-0 w-[20px]"
                              :style {:color "currentColor"}}
                     [:img {:src (str config/webapp-url "/icons/icon-jira-current-color.svg")}]]]]]])

              [:div {:class "relative group"}
               [:> Tooltip {:content "Copy link"}
                [:div {:class "rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer copy-to-clipboard-url"
                       :data-clipboard-text (str (-> js/document .-location .-origin)
                                                 (routes/url-for :sessions)
                                                 "/" (:id session))}
                 [:> hero-outline-icon/ClipboardDocumentIcon {:class "h-5 w-5 text-gray-600"}]]]]

              (when (and (= (:verb session) "exec")
                         (or (:output session) (:event_stream session))
                         (not disabled-download))
                [:div {:class "relative"}
                 [:> Tooltip {:content (str "Download "
                                            (cs/upper-case
                                             (get export-dictionary (keyword (:type session)) "txt")))}
                  [:div {:class "relative rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer group"
                         :on-click #(rf/dispatch [:audit->session-file-generate
                                                  (:id session)
                                                  (get export-dictionary (keyword (:type session)) "txt")])}
                   [:> hero-outline-icon/ArrowDownTrayIcon {:class "h-5 w-5 text-gray-600"}]]]])]]

            (when (-> session :labels :runbookFile)
              [:div {:class "text-xs text-gray-500"}
               "Runbook: " (-> session :labels :runbookFile)])]

           [:section {:class "grid grid-cols-1 gap-6 lg:grid-cols-4"}
            [:div {:class "col-span-1 flex gap-large items-center"}
             [:div {:class "flex flex-grow gap-regular items-center"}
              [user-icon/initials-black session-user-name]
              [:span
               {:class "text-gray-800 text-sm"}
               session-user-name]]]

            [:div {:class (str "flex flex-col gap-small self-center justify-center"
                               " rounded-lg bg-gray-100 p-3")}
             [:div
              {:class "flex items-center gap-regular text-xs"}
              [:span
               {:class "flex-grow text-gray-500"}
               "start:"]
              [:span
               (formatters/time-parsed->full-date start-date)]]
             (when-not (and
                        (= verb "exec")
                        in-progress?)
               [:div
                {:class "flex items-center justify-end gap-regular text-xs"}
                [:span
                 {:class "flex-grow text-gray-500"}
                 "end:"]
                [:span
                 (formatters/time-parsed->full-date end-date)]])
             (when (and (= verb "connect")
                        (get-in session [:review :revoke_at]))
               [:div
                {:class "flex items-center justify-end gap-regular text-xs"}
                [:span
                 {:class "flex-grow text-gray-500"}
                 "access until:"]
                [:span
                 (formatters/time-parsed->full-date (get-in session [:review :revoke_at]))]])]

            (when has-review?
              [:<>
               [:> Flex {:direction "column" :gap "1"}
                [:> Text {:size "2" :weight "bold" :class "text-gray-12"}
                 "Reviewers"]
                [:> Text {:size "2" :class "text-gray-11"}
                 (cs/join ", " (map :group review-groups))]]

               [:> Flex {:direction "column" :gap "1"}
                [:> Text {:size "2" :weight "bold" :class "text-gray-12"}
                 "Status"]
                [:> Flex {:direction "column" :gap "1"}
                 (if all-groups-pending?
                   [:> Flex {:gap "1" :align "center"}
                    [:> Clock2 {:size 16 :class "text-gray-11"}]
                    [:> Text {:size "2" :class "text-gray-11"}
                     "Pending"]]

                   (for [group review-groups]
                     ^{:key (:id group)}
                     [review-status-text (:status group) (:group group)]))]]])]

           ;; parallel mode batch
           (when session-batch-id
             [:> Flex {:align "center" :gap "2"}
              [:> Text {:size "2" :weight "bold" :class "text-gray-12"}
               "Parallel mode batch:"]
              [:> Button {:size "1"
                          :variant "soft"
                          :on-click #(js/open
                                      (str (-> js/document .-location .-origin)
                                           (routes/url-for :sessions-list-filtered-by-ids)
                                           "?batch_id=" session-batch-id)
                                      "_blank")}
               "Open"
               [:> ArrowUpRight {:size 16}]]])

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
                         (some #(= "PENDING" (:status %))
                               review-groups))
             [:section {:id "session-event-stream"
                        :class "max-h-[700px]"}
              (if (= (:status @session-details) :loading)
                [loading-player]

                [:<>
                 (if has-large-payload?
                   [large-payload-warning
                    {:session session}]

                   [:div {:class "h-full px-small"}
                    (if (= (:verb session) "exec")
                      [results-container/main
                       connection-subtype
                       {:results (sanitize-response
                                  (utilities/decode-b64 (or (first (:event_stream session)) ""))
                                  connection-subtype)
                        :results-status (:status @session-details)
                        :fixed-height? true
                        :results-id (:id session)
                        :not-clipboard? disabled-download}]
                      [session-event-stream (:type session) session])])])])

           ;; action buttons section
           (when can-review?
             [:> Flex {:justify "end" :gap "2"}
              [:> Button {:color "red" :size "2" :variant "soft" :on-click handle-reject}
               "Reject"]

              ;; Approve dropdown
              [:> DropdownMenu.Root
               [:> DropdownMenu.Trigger
                [:> Button {:size "2" :color "green"}
                 "Approve"
                 [:> ChevronDown {:size 16}]]]
               [:> DropdownMenu.Content
                [:> DropdownMenu.Item {:class "flex justify-between gap-4 group"
                                       :on-click open-time-window-modal}
                 "Approve in a Time Window"
                 [:> CalendarClock {:size 16 :class "text-gray-10 group-hover:text-white"}]]
                [:> DropdownMenu.Item {:class "flex justify-between gap-4 group"
                                       :on-click handle-approve}
                 "Approve"
                 [:> Check {:size 16 :class "text-gray-10 group-hover:text-white"}]]]]])

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
               "Execute"]])])

        (finally
          (.destroy clipboard-url)
          (rf/dispatch [:audit->clear-session])
          (rf/dispatch [:reports->clear-session-report-by-id]))))))

