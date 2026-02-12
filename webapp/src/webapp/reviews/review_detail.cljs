(ns webapp.reviews.review-detail
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout DropdownMenu Flex
                               Text Tooltip ScrollArea]]
   ["lucide-react" :refer [CalendarClock Check CheckCheck ChevronDown
                           Download Info Clock2 X Share2]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.audit.views.time-window-modal :as time-window-modal]
   [webapp.components.headings :as h]
   [webapp.components.user-icon :as user-icon]
   [webapp.config :as config]
   [webapp.formatters :as formatters]
   [webapp.routes :as routes]))

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

(defmulti ^:private review-status-icon identity)
(defmethod ^:private review-status-icon "PENDING" [] "waiting-circle-yellow")
(defmethod ^:private review-status-icon "APPROVED" [] "check-black")
(defmethod ^:private review-status-icon "REJECTED" [] "close-red")

(defn review-details-page [session]
  (let [user-details (rf/subscribe [:users->current-user])
        session-details (rf/subscribe [:reviews-plugin->review-details])
        clipboard-disabled? (rf/subscribe [:gateway->clipboard-disabled?])
        connection-details (rf/subscribe [:connections->connection-details])]
    (when session
      (rf/dispatch [:reviews-plugin->get-review-by-id session]))
    (fn [_]
      (let [user (:data @user-details)
            current-session (:review @session-details)
            user-name (:user_name current-session)
            connection-name (:connection current-session)
            review (:review current-session)
            review-groups-data (:review_groups_data review)
            review-status (:status review)
            session-type (:type current-session)
            start-date (:start_date current-session)
            end-date (:end_date current-session)
            all-groups-pending? (every? #(= (:status %) "PENDING") review-groups-data)
            has-review? (boolean (seq (-> session :review)))
            _ (when (and has-review?
                         connection-name
                         (not (:loading @connection-details))
                         (not= (:name (:data @connection-details)) connection-name))
                (rf/dispatch [:connections->get-connection-details connection-name]))
            ready? (= (:status session) "ready")
            verb (:verb current-session)
            review-groups (-> session :review :review_groups_data)
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
            has-large-input? (:has-large-input? @session-details)
            in-progress? (or (= end-date nil)
                             (= end-date ""))
            current-path (.-pathname (.-location js/window))
            is-review-page? (= current-path "/reviews")
            ;; Check if we're on a dedicated review page (e.g., /reviews/{id})
            is-dedicated-page? (cs/starts-with? current-path "/reviews/")
            review-url (str (-> js/document .-location .-origin)
                            (routes/url-for :reviews-plugin)
                            "/" (-> current-session :review :id))
            copy-to-clipboard (fn []
                                (-> (js/navigator.clipboard.writeText review-url)
                                    (.then #(rf/dispatch [:show-snackbar {:level :success :text "URL copied to clipboard"}]))
                                    (.catch #(js/console.error "Failed to copy:" %))))]
        [:div {:class (str "flex flex-col gap-regular h-full "
                           (when is-review-page? "max-h-[800px]")
                           (when (not is-review-page?) "max-h-[9S00px]"))}
         ;; Header
         [:header {:class "mb-regular"}
          [:div {:class "flex"}
           [:div {:class "flex flex-col lg:flex-row flex-grow gap-small lg:items-baseline"}
            [:div {:class "flex flex-col"}
             [h/h2 connection-name]
             [:div {:class "text-sm flex flex-grow gap-regular"}
              [:span {:class "text-gray-500"}
               "type:"]
              [:span {:class "font-bold"}
               session-type]]]]

           [:div {:class "relative flex gap-2.5 items-start"}
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

            (when-not @clipboard-disabled?
              [:> Tooltip {:content "Copy link"}
               [:div {:class "rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer"
                      :on-click copy-to-clipboard}
                [:> Share2 {:size 20 :class "text-gray-11"}]]])

            ;; Only show close button when in modal context (not on dedicated page)
            (when-not is-dedicated-page?
              [:> Tooltip {:content "Close"}
               [:div {:class "rounded-full p-2 bg-gray-3 hover:bg-gray-4 transition cursor-pointer"
                      :on-click #(rf/dispatch [:modal->close])}
                [:> X {:size 20 :class "text-gray-11"}]]])]]]

         ;; Information Grid
         [:section {:class "grid grid-cols-1 gap-regular pb-regular lg:grid-cols-4"}
          [:div {:class "col-span-1 flex gap-large items-center"}
           [:div {:class "flex flex-grow gap-regular items-center"}
            [user-icon/initials-black user-name]
            [:span
             {:class "text-gray-800 text-sm"}
             user-name]]]

          [:div {:class (str "flex flex-col gap-small self-center justify-center"
                             " rounded-lg bg-gray-100 p-3")}
           [:div
            {:class "flex items-center gap-regular text-xs"}
            [:span
             {:class "flex-grow text-gray-500"}
             "start:"]
            [:span
             (formatters/time-parsed->full-date start-date)]]
           (when-not (and (= verb "exec") in-progress?)
             [:div
              {:class "flex items-center justify-end gap-regular text-xs"}
              [:span
               {:class "flex-grow text-gray-500"}
               "end:"]
              [:span
               (formatters/time-parsed->full-date end-date)]])
           (when (> (or (:access_duration review) 0) 0)
             [:div {:class "flex items-center gap-small"}
              [:span {:class "text-gray-500"}
               "session time:"]
              [:span {:class "font-bold"}
               (formatters/time-elapsed (/ (:access_duration review) 1000000))]])]

          ;; Reviewers section
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
                   [review-status-icon (:status group) group]))]]])]

         ;; Script section
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
                 end-time (formatters/utc-time->display-time end-time-utc)]
             [:> Flex {:align "center" :justify "end" :gap "4"}
              [:> Text {:size "2" :class "text-gray-11"}
               "This session is ready and available to be executed from "
               [:> Text {:size "2" :weight "medium" :class "text-gray-11"}
                start-time]
               " to "
               [:> Text {:size "2" :weight "medium" :class "text-gray-11"}
                end-time]
               "."]]))]))))

(defmulti item-view identity)
(defmethod item-view :opened [_ review-details]
  (review-details-page (:review review-details)))

(defmethod item-view :default [_]
  [:div.flex.justify-center.items-center.h-full
   [:span.text-xl.text-gray-400 "No review selected"]])

(defmethod item-view :loading [_ task-details]
  (item-view :opened task-details))

(defn review-detail []
  (let [active-review (rf/subscribe [:reviews-plugin->review-details])]
    (fn []
      [item-view
       (:status @active-review)
       @active-review])))
