(ns webapp.audit.views.session-details
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["clipboard" :as clipboardjs]
            ["react" :as react]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.audit.views.results-container :as results-container]
            [webapp.audit.views.session-data-raw :as session-data-raw]
            [webapp.audit.views.session-data-video :as session-data-video]
            [webapp.components.button :as button]
            [webapp.components.headings :as h]
            [webapp.components.icon :as icon]
            [webapp.components.loaders :as loaders]
            [webapp.components.popover :as popover]
            [webapp.components.user-icon :as user-icon]
            [webapp.formatters :as formatters]
            [webapp.utilities :as utilities]))

(def ^:private export-dictionary
  {:postgres "csv"
   :mysql "csv"
   :database "csv"
   :custom "txt"
   :command-line "txt"})

(defn- loading-player []
  [:div {:class "flex gap-small items-center justify-center py-large"}
   [:span {:class "italic text-xs text-gray-600"}
    "Loading data for this session"]
   [loaders/simple-loader {:size 4}]])

(defmulti ^:private session-event-stream identity)
(defmethod ^:private session-event-stream "command-line"
  [_ session]
  (let [event-stream (:event_stream session)]
    [session-data-video/main event-stream]))

(defmethod ^:private session-event-stream "custom"
  [_ session]
  (let [event-stream (:event_stream session)]
    [session-data-video/main event-stream]))

(defmethod ^:private session-event-stream :default
  [_ session]
  (let [start-date (:start_date session)
        event-stream (:event_stream session)]
    [session-data-raw/main event-stream start-date]))

;; indentifies if a session is a runbook or not and re execute it
(defn- re-run-session [session]
  (if (-> session :labels :runbookFile)
    (do
      (rf/dispatch [:runbooks-plugin->run-runbook
                    {:file-name (-> session :labels :runbookFile)
                     :params (js/JSON.parse (-> session :labels :runbookParameters))
                     :connection-name (:connection session)}])
      (rf/dispatch [:audit->clear-session-details-state {:status :loading}]))
    (rf/dispatch [:audit->re-run-session session])))

(defmulti ^:private review-status-icon identity)
(defmethod ^:private review-status-icon "PENDING" [] "waiting-circle-yellow")
(defmethod ^:private review-status-icon "APPROVED" [] "check-black")
(defmethod ^:private review-status-icon "REJECTED" [] "close-red")

(defn- add-review-popover [add-review-cb]
  [:div
   {:class "flex gap-small p-regular"}
   [button/secondary {:text "Reject"
                      :variant :small
                      :on-click #(add-review-cb "rejected")}]
   [button/primary {:text "Approve"
                    :variant :small
                    :on-click #(add-review-cb "approved")}]])

(defn- review-group-item [group session]
  (let [add-review-popover-open? (r/atom false)
        add-review (fn [status]
                     (rf/dispatch [:audit->add-review
                                   session
                                   status
                                   (:group group)])
                     (reset! add-review-popover-open? false))]
    (fn [group _]
      [:div
       {:class (str "relative flex flex-grow items-center gap-small"
                    " text-xs")}
       [icon/regular {:size 4
                      :icon-name "user-group"}]
       [:span {:class "flex-grow"} (:group group)]
       [:span
        {:class "text-xxs italic text-gray-500 text-right"}
        (:status group)]
       [icon/regular {:size 4
                      :icon-name (review-status-icon
                                  (cs/upper-case (:status group)))}]
       [:span {:class "w-5"}]
       [popover/right {:open @add-review-popover-open?
                       :component [add-review-popover add-review]
                       :on-click-outside #(reset! add-review-popover-open? false)}]])))

(defn data-masking-analytics [session-report]
  (let [redacted-types (map #(utilities/sanitize-string (:info_type %))
                            (-> session-report :data :items))
        total-redact (-> session-report :data :total_redact_count)
        count-less-1 (- (count redacted-types) 1)]
    [:> ui/Disclosure
     (fn [params]
       (r/as-element
        [:<>
         [:> (.-Button ui/Disclosure)
          {:class (str "w-full flex justify-between items-center gap-small bg-purple-50 p-3 rounded-t-md "
                       "text-md font-semibold focus:outline-none focus-visible:ring text-sm "
                       "focus-visible:ring-gray-500 focus-visible:ring-opacity-75")}
          [:div {:class "flex items-center gap-small"}
           [:> hero-solid-icon/SparklesIcon {:class "text-purple-500 h-5 w-5 shrink-0"
                                             :aria-hidden "true"}]
           "AI Data Masking"]

          [:div {:class "flex items-center gap-regular"}
           (when-not (.-open params)
             [:div
              [:span
               "Redacted Types: "]
              [:span {:class "font-normal"}
               (str (count redacted-types)
                    " (" (first redacted-types)
                    (if (>= count-less-1 1)
                      (str  " + "
                            count-less-1
                            " more)")
                      ")"))]])
           (when-not (.-open params)
             [:div
              [:span
               "Total Items: "]
              [:span {:class "font-normal"}
               total-redact]])

           [:> hero-solid-icon/ChevronDownIcon {:class (str (when (.-open params) "rotate-180 transform ")
                                                            "text-dark-900 h-5 w-5 shrink-0")
                                                :aria-hidden "true"}]]]
         [:> (.-Child ui/Transition) {:as react/Fragment
                                      :enter "transform transition ease-in duration-[200ms]"
                                      :enterFrom "opacity-0 -translate-y-6"
                                      :enterTo "opacity-100 translate-y-0"
                                      :leave "transform duration-200 transition ease-in duration-[200ms]"
                                      :leaveFrom "opacity-100 translate-y-0"
                                      :leaveTo "opacity-0 -translate-y-6"}
          [:> (.-Panel ui/Disclosure) {:className "bg-purple-50 p-2 rounded-b-md"}
           [:div {:class "grid grid-cols-2 gap-2 text-xs"}
            [:div {:class "flex flex-col justify-center items-center gap-1 rounded-md bg-purple-100 p-2"}
             [:> hero-solid-icon/NewspaperIcon {:class "text-purple-300 h-5 w-5 shrink-0"
                                                :aria-hidden "true"}]
             [:span
              "Redacted Types"]
             [:span {:class "font-semibold"}
              (cs/join ", " redacted-types)]]
            [:div {:class "flex flex-col justify-center items-center gap-1 rounded-md bg-purple-100 p-2"}
             [:> hero-solid-icon/CheckBadgeIcon {:class "text-purple-300 h-5 w-5 shrink-0"
                                                 :aria-hidden "true"}]
             [:span
              "Total Redacted Data"]
             [:span {:class "font-semibold"}
              (str total-redact " "
                   (if (<= total-redact 1) "item" "items"))]]]]]]))]))

(defn- tooltip [text]
  [:div {:class "absolute -bottom-10 left-1/2 flex-col hidden mt-6 w-max group-hover:flex items-center -translate-x-1/2"}
   [:div {:class "w-3 h-3 -mb-2 bg-gray-900 transform rotate-45"}]
   [:span {:class (str "relative bg-gray-900 rounded-md z-50 "
                       "py-1.5 px-3.5 text-xs text-white leading-none whitespace-no-wrap shadow-lg")}
    text]])

(defn main [session]
  (let [user (rf/subscribe [:users->current-user])
        session-details (rf/subscribe [:audit->session-details])
        session-report (rf/subscribe [:reports->session])
        executing-status (r/atom :ready)
        add-review-popover-open? (r/atom false)
        clipboard-url (new clipboardjs ".copy-to-clipboard-url")]
    (when session
      (rf/dispatch [:audit->get-session-by-id session])
      (rf/dispatch [:reports->get-report-by-session-id session]))
    (fn []
      (let [session (:session @session-details)
            user-name (:user_name session)
            connection-name (:connection session)
            start-date (:start_date session)
            end-date (:end_date session)
            review-groups (-> session :review :review_groups_data)
            in-progress? (or (= end-date nil)
                             (= end-date ""))
            has-review? (boolean (seq (-> session :review)))
            has-session-report? (seq (-> @session-report :data :items))
            ready? (= (:status session) "ready")
            can-review? (and
                         (some #(= "PENDING" (:status %))
                               review-groups)
                         (some (fn [review-group]
                                 (some #(= (:group review-group) %)
                                       (-> @user :data :groups)))
                               review-groups))
            add-review-cb (fn [status]
                            (rf/dispatch [:audit->add-review
                                          session
                                          status])
                            (reset! add-review-popover-open? false))
            script-data (-> session :script :data)
            metadata (-> session :metadata)
            runbook-params (js->clj
                            (js/JSON.parse (-> session :labels :runbookParameters))
                            :keywordize-keys true)
            _ (.on clipboard-url "success" #(rf/dispatch [:show-snackbar {:level :success :text "URL copied to clipboard"}]))]
        [:div
         [:header {:class "mb-regular mr-large"}
          [:div {:class "flex"}
           [:div {:class "flex flex-col lg:flex-row flex-grow gap-small lg:items-baseline"}
            [:div {:class "flex flex-col"}
             [h/h2 connection-name]
             [:div {:class "text-sm flex flex-grow gap-regular"}
              [:span {:class "text-gray-500"}
               "type:"]
              [:span {:class "font-bold"}
               (:type session)]]]

            (when (and in-progress? (not ready?))
              [:div {:class "flex gap-small lg:justify-end items-center h-full lg:ml-large"}
               [:div {:class (str "rounded-full w-1.5 h-1.5 bg-green-500")}]
               [:span {:class "text-xs text-gray-500"}
                "This session has pending items"]])
            (when ready?
              [:div {:class "flex gap-regular justify-end items-center mx-large"}
               [:span {:class "text-xs text-gray-500"}
                "This session is ready to be executed"]
               [button/primary {:text "Execute"
                                :status @executing-status
                                :on-click (fn []
                                            (reset! executing-status :loading)
                                            (rf/dispatch [:audit->execute-session session]))
                                :variant :small}]])]

           [:div {:class "relative flex gap-2.5 items-start pr-3"}
            [:div {:class "relative group"}
             [tooltip "Re-run"]
             [:div {:class "rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer"
                    :on-click #(re-run-session session)}
              [:> hero-outline-icon/PlayIcon {:class "h-5 w-5 text-gray-600"}]]]

            [:div {:class "relative group"}
             [tooltip "Copy link"]
             [:div {:class "rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer copy-to-clipboard-url"
                    :data-clipboard-text (str (-> js/document .-location .-origin) "/sessions/" (:id session))}
              [:> hero-outline-icon/ClipboardDocumentIcon {:class "h-5 w-5 text-gray-600"}]]]

            (when (and (= (:verb session) "exec") (or (:output session) (:event_stream session)))
              [:div {:class "relative"}
               [:div {:class "relative rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer group"
                      :on-click #(rf/dispatch [:audit->session-file-generate
                                               (:id session)
                                               (get export-dictionary (keyword (:type session)) "txt")])}
                [tooltip (str "Download " (cs/upper-case
                                           (get export-dictionary (keyword (:type session)) "txt")))]
                [:> hero-outline-icon/ArrowDownTrayIcon {:class "h-5 w-5 text-gray-600"}]]])]]

          (when (-> session :labels :runbookFile)
            [:div {:class "text-xs text-gray-500"}
             "Runbook: " (-> session :labels :runbookFile)])]

         [:section {:class "grid grid-cols-1 lg:grid-cols-3 gap-regular pb-regular"}
          [:div {:class "col-span-1 flex gap-large items-center"}
           [:div {:class "flex flex-grow gap-regular items-center"}
            [user-icon/initials-black user-name]
            [:span
             {:class "text-gray-800 text-sm"}
             user-name]]]
          [:div
           {:class (str "flex flex-col gap-small justify-center"
                        " rounded-lg bg-gray-100 p-regular")}
           [:div
            {:class "flex items-center gap-regular text-xs"}
            [:span
             {:class "flex-grow text-gray-500"}
             "start:"]
            [:span
             (formatters/time-parsed->full-date start-date)]]
           (when-not in-progress?
             [:div
              {:class "flex items-center justify-end gap-regular text-xs"}
              [:span
               {:class "flex-grow text-gray-500"}
               "end:"]
              [:span
               (formatters/time-parsed->full-date end-date)]])]
          [:div {:id "session-reviews"}
           [:header {:class "relative flex text-xs text-gray-800 mb-small"}
            [:span {:class "flex-grow font-bold"} "Reviewers"]
            [:<>
             (when can-review?
               [:span {:class (str "flex items-center cursor-pointer "
                                   "text-xxs text-blue-500 font-semibold")
                       :on-click #(reset! add-review-popover-open? true)}
                [:span "Add your review"]
                [icon/regular {:size 5
                               :icon-name "cheveron-down-blue"}]])

             [popover/right {:open @add-review-popover-open?
                             :component [add-review-popover add-review-cb]
                             :on-click-outside #(reset! add-review-popover-open? false)}]]]

           (when (nil? (-> session :review))
             [:div
              {:class "py-small text-xs italic text-gray-500 text-left"}
              "No review info"])
           [:div {:class (str "rounded-lg "
                              "flex flex-col")}
            (doall
             (for [group review-groups]
               ^{:key (:id group)}
               [review-group-item group session @user]))]]]

         ;; runbook params
         (when (and runbook-params
                    (seq runbook-params))
           [:div {:class "flex gap-regular items-center mb-regular py-small border-b border-t"}
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
           [:div {:class " mb-regular"}
            (doall
             (for [[metadata-key metadata-value] metadata]
               ^{:key metadata-key}
               [:div {:class "flex gap-small items-center py-small border-t last:border-b"}
                [:header {:class "w-32 px-small text-sm font-bold"}
                 metadata-key]
                [:section {:class "w-full text-xs border-l p-small"}
                 [:span metadata-value]]]))])
         ;; end metadata

         ;; script area
         (when (and script-data
                    (> (count script-data) 0))
           [:section {:id "session-script"}
            [:div
             {:class (str "w-full max-h-40 overflow-auto p-regular whitespace-pre "
                          "rounded-lg bg-gray-100 "
                          "text-xs text-gray-800 font-mono")}
             [:article script-data]]])
         ;; end script area

         ;; data masking analytics
         (when-not (or has-review?
                       (= :loading (:status @session-report))
                       (not has-session-report?))
           [:div {:class "mt-6"}
            [data-masking-analytics @session-report]])
         ;; end data masking analytics

         [:section {:id "session-event-stream"
                    :class "pt-regular"}
          (if (= (:status @session-details) :loading)
            [loading-player]

            [:div {:class "h-full px-small"}
             (if (= (:verb session) "exec")
               [results-container/main
                {:results (first (:event_stream session))
                 :results-status (:status @session-details)
                 :fixed-height? true
                 :results-id (:id session)
                 :connection-type (:type session)}]
               [session-event-stream (:type session) session])])]]))))

