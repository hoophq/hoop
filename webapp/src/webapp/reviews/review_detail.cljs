(ns webapp.reviews.review-detail
  (:require
   ["@heroicons/react/24/outline" :as hero-outline-icon]
   ["@radix-ui/themes" :refer [Box Tooltip]]
   ["clipboard" :as clipboardjs]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.button :as button]
   [webapp.components.headings :as h]
   [webapp.components.icon :as icon]
   [webapp.components.popover :as popover]
   [webapp.components.tooltip :as tooltip]
   [webapp.components.user-icon :as user-icon]
   [webapp.config :as config]
   [webapp.formatters :as formatters]
   [webapp.routes :as routes]))

(defn- add-review-popover [add-review-cb]
  [:div
   {:class "flex gap-small p-regular"}
   [button/secondary {:text "Reject"
                      :variant :small
                      :on-click #(add-review-cb "rejected")}]
   [button/primary {:text "Approve"
                    :variant :small
                    :on-click #(add-review-cb "approved")}]])

(defmulti ^:private review-status-icon identity)
(defmethod ^:private review-status-icon "PENDING" [] "waiting-circle-yellow")
(defmethod ^:private review-status-icon "APPROVED" [] "check-black")
(defmethod ^:private review-status-icon "REJECTED" [] "close-red")

(defn- review-group-item [group session user]
  (let [add-review-popover-open? (r/atom false)
        add-review (fn [status]
                     (rf/dispatch [:reviews-plugin->add-review
                                   session
                                   status
                                   (:group group)])
                     (reset! add-review-popover-open? false))]
    [:> Box {:class "flex w-full relative items-center gap-small text-xs"}
     [:> Box
      [icon/regular {:size 4
                     :icon-name "user-group"}]]
     [tooltip/truncate-tooltip {:text (:group group)}]
     [:> Box
      [:span {:class "text-xxs italic text-gray-500 text-right"}
       (:status group)]
      (when (or (= (:status group) "APPROVED")
                (= (:status group) "REJECTED"))
        [:> Box {:class "text-xxs italic text-gray-500 text-right max-w-[100px]"}
         [tooltip/truncate-tooltip {:text (-> group :reviewed_by :email)}]])]
     [:> Box
      [icon/regular {:size 4
                     :icon-name (review-status-icon
                                 (cs/upper-case (:status group)))}]]
     [popover/right {:open @add-review-popover-open?
                     :component [add-review-popover add-review]
                     :on-click-outside #(reset! add-review-popover-open? false)}]]))

(defn review-details-page [session]
  (let [user (rf/subscribe [:users->current-user])
        session-details (rf/subscribe [:reviews-plugin->review-details])
        clipboard-disabled? (rf/subscribe [:gateway->clipboard-disabled?])
        add-review-popover-open? (r/atom false)]
    (when session
      (rf/dispatch [:reviews-plugin->get-review-by-id session]))
    (fn [_]
      (r/with-let [clipboard-url (when-not @clipboard-disabled?
                                   (new clipboardjs ".copy-to-clipboard-url"))
                   _ (when clipboard-url
                       (.on clipboard-url "success" #(rf/dispatch [:show-snackbar {:level :success :text "URL copied to clipboard"}])))]
        (let [current-session (:review @session-details)
              user-name (:user_name current-session)
              connection-name (:connection current-session)
              review (:review current-session)
              review-groups-data (:review_groups_data review)
              review-status (:status review)
              session-type (:type current-session)
              start-date (:start_date current-session)
              end-date (:end_date current-session)
              verb (:verb current-session)

              can-review? (and
                           (= "PENDING" review-status)
                           (some #(= "PENDING" (:status %))
                                 review-groups-data)
                           (some (fn [review-group]
                                   (some #(= (:group review-group) %)
                                         (-> @user :data :groups)))
                                 review-groups-data))
              add-review-cb (fn [status]
                              (rf/dispatch [:reviews-plugin->add-review
                                            current-session
                                            status])
                              (reset! add-review-popover-open? false))
              in-progress? (or (= end-date nil)
                               (= end-date ""))
              current-path (.-pathname (.-location js/window))
              is-review-page? (= current-path "/reviews")]
          [:div {:class (str "flex flex-col gap-regular h-full "
                             (when is-review-page? "max-h-[800px]")
                             (when (not is-review-page?) "max-h-[9S00px]"))}
           ;; Header
           [:header {:class "mb-regular mr-large"}
            [:div {:class "flex"}
             [:div {:class "flex flex-col lg:flex-row flex-grow gap-small lg:items-baseline"}
              [:div {:class "flex flex-col"}
               [h/h2 connection-name]
               [:div {:class "text-sm flex flex-grow gap-regular"}
                [:span {:class "text-gray-500"}
                 "type:"]
                [:span {:class "font-bold"}
                 session-type]]]]

             [:div {:class "relative flex gap-2.5 items-start pr-3"}
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
                [:div {:class "relative group"}
                 [:> Tooltip {:content "Copy link"}
                  [:div {:class "rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer copy-to-clipboard-url"
                         :data-clipboard-text (str (-> js/document .-location .-origin)
                                                   (routes/url-for :reviews-plugin)
                                                   "/" (-> current-session :review :id))}
                   [:> hero-outline-icon/ClipboardDocumentIcon {:class "h-5 w-5 text-gray-600"}]]]])]]]

           ;; Information Grid
           [:section {:class "grid grid-cols-1 gap-regular pb-regular lg:grid-cols-3"}
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
            [:div {:id "session-reviews" :class "self-center"}
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

             (when (empty? review-groups-data)
               [:div
                {:class "py-small text-xs italic text-gray-500 text-left"}
                "No review info"])
             [:div {:class "rounded-lg w-full flex flex-col gap-2"}
              (doall
               (for [group review-groups-data]
                 ^{:key (:id group)}
                 [review-group-item group current-session @user]))]]]

           ;; Script section
           (when (not (cs/blank? (-> current-session :script :data)))
             [:div
              {:class (str "w-full overflow-auto p-regular whitespace-pre "
                           "rounded-lg bg-gray-100 "
                           "text-xs text-gray-800 font-mono")}
              [:article (-> current-session :script :data)]])])

        (finally
          (when clipboard-url
            (.destroy clipboard-url)))))))

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
