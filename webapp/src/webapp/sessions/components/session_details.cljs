(ns webapp.sessions.components.session-details
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex Text]]
   ["lucide-react" :refer [Rotate3d Package ChevronDown ChevronUp CircleUser
                           CalendarArrowUp CalendarArrowDown FastForward
                           Clock2 CircleCheckBig OctagonX ExternalLink BadgeCheck]]
   [reagent.core :as r]
   [webapp.formatters :as formatters]
   [webapp.routes :as routes]))

;; Status badge helpers
(defmulti ^:private access-request-badge identity)
(defmethod ^:private access-request-badge "APPROVED" [_]
  [:> Badge {:color "green" :size "2"}
   [:> Flex {:gap "1" :align "center"}
    [:> CircleCheckBig {:size 14}]
    "Approved"]])

(defmethod ^:private access-request-badge "PENDING" [_]
  [:> Badge {:color "yellow" :size "2"}
   [:> Flex {:gap "1" :align "center"}
    [:> Clock2 {:size 14}]
    "Pending"]])

(defmethod ^:private access-request-badge "REJECTED" [_]
  [:> Badge {:color "red" :size "2"}
   [:> Flex {:gap "1" :align "center"}
    [:> OctagonX {:size 14}]
    "Rejected"]])

(defmethod ^:private access-request-badge :default [_]
  nil)

(defmulti ^:private status-badge identity)
(defmethod ^:private status-badge "done" [_]
  [:> Badge {:color "green" :size "2"}
   "Success"])

(defmethod ^:private status-badge "ready" [_]
  [:> Badge {:color "blue" :size "2"}
   "Ready"])

(defmethod ^:private status-badge "error" [_]
  [:> Badge {:color "red" :size "2"}
   "Error"])

(defmethod ^:private status-badge :default [status]
  [:> Badge {:color "gray" :size "2"}
   (or status "Unknown")])

(defn- detail-row [{:keys [icon label value show-gradient?]}]
  [:> Box {:class (str "relative " (when show-gradient? "after:content-[''] after:absolute after:bottom-0 after:left-0 after:right-0 after:h-8 after:bg-gradient-to-b after:from-transparent after:via-transparent after:to-white after:pointer-events-none"))}
   [:> Flex {:align "center"}
    [:> Flex {:gap "2" :align "center" :class "w-40"}
     (when icon
       [:> Box {:class "text-gray-11"}
        icon])
     [:> Text {:size "2" :class "text-gray-11"}
      label]]
    [:> Box
     value]]])

(defn main []
  (let [expanded? (r/atom false)]
    (fn [{:keys [session review-groups]}]
      (let [connection-name (:connection session)
            connection-role (str (:connection session) "-" (:type session))
            session-status (:status session)
            start-date (:start_date session)
            end-date (:end_date session)
            user-name (:user_name session)
            session-batch-id (:session_batch_id session)
            jira-url (get-in session [:integrations_metadata :jira_issue_url])
            has-review? (boolean (seq review-groups))
            all-groups-pending? (every? #(= (:status %) "PENDING") review-groups)]
        [:> Box
         [:> Box {:class "space-y-radix-4"}

          ;; Always visible fields
          [detail-row {:label "Resource"
                       :icon [:> Package {:size 20}]
                       :value [:> Flex {:gap "2" :align "center"}
                               [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                                connection-name]
                               (when has-review?
                                 (if all-groups-pending?
                                   [:> Badge {:color "yellow" :size "1"}
                                    [:> Flex {:gap "1" :align "center"}
                                     [:> Clock2 {:size 12}]
                                     "Pending"]]
                                   (for [group review-groups]
                                     ^{:key (:id group)}
                                     (when (not= (:status group) "PENDING")
                                       (access-request-badge (:status group))))))]}]

          [detail-row {:label "Role"
                       :icon [:> Rotate3d {:size 20}]
                       :value [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                               connection-role]}]

          (when has-review?
            [detail-row {:label "Access Request"
                         :icon [:> CircleCheckBig {:size 20}]
                         :value (if all-groups-pending?
                                  [access-request-badge "PENDING"]
                                  [:> Flex {:gap "1" :wrap "wrap"}
                                   (for [group review-groups]
                                     ^{:key (:id group)}
                                     [access-request-badge (:status group)])])}])

          [detail-row {:label "Status"
                       :icon [:> BadgeCheck {:size 20}]
                       :show-gradient? (not @expanded?)
                       :value [status-badge session-status]}]

          ;; Conditionally visible fields (when expanded) with animation
          [:> Box {:class (str "overflow-hidden transition-all duration-300 ease-in-out "
                               (if @expanded?
                                 "max-h-[1000px] opacity-100"
                                 "max-h-0 opacity-0"))}
           [:> Box {:class "space-y-radix-4"}
            [detail-row {:icon [:> CircleUser {:size 20}]
                         :label "Created by"
                         :value [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                                 user-name]}]

            [detail-row {:icon [:> CircleUser {:size 20}]
                         :label "Executed by"
                         :value [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                                 user-name]}]

            [detail-row {:icon [:> CalendarArrowUp {:size 20}]
                         :label "Created at"
                         :value [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                                 (formatters/time-parsed->full-date start-date)]}]

            (when end-date
              [detail-row {:icon [:> CalendarArrowDown {:size 20}]
                           :label "Finished at"
                           :value [:> Text {:size "2" :weight "medium" :class "text-gray-12"}
                                   (formatters/time-parsed->full-date end-date)]}])

            (when jira-url
              [detail-row {:icon [:> ExternalLink {:size 20}]
                           :label "Integrations"
                           :value [:> Flex {:gap "2" :align "center"}
                                   [:a {:href jira-url
                                        :target "_blank"
                                        :class "text-blue-11 hover:text-blue-12 underline"}
                                    [:> Text {:size "2" :weight "medium"}
                                     "Open in Jira"]]]}])

            (when session-batch-id
              [detail-row {:icon [:> FastForward {:size 20}]
                           :label "Parallel Sessions"
                           :value [:> Flex {:gap "2" :align "center"}
                                   [:a {:href (str (-> js/document .-location .-origin)
                                                   (routes/url-for :sessions-list-filtered-by-ids)
                                                   "?batch_id=" session-batch-id)
                                        :target "_blank"
                                        :class "text-blue-11 hover:text-blue-12 underline"}
                                    [:> Text {:size "2" :weight "medium"}
                                     "Open Parallel Summary"]]]}])]]]

         ;; See more / See less button - left aligned
         [:> Box {:class "mt-4"}
          [:> Flex {:justify "start"}
           [:button {:class "flex items-center gap-2 text-gray-11 hover:text-gray-12 transition cursor-pointer"
                     :on-click #(swap! expanded? not)}
            [:> Text {:size "2" :weight "medium"}
             (if @expanded? "See less" "See more")]
            (if @expanded?
              [:> ChevronUp {:size 16}]
              [:> ChevronDown {:size 16}])]]]]))))
