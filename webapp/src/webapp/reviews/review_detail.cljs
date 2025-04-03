(ns webapp.reviews.review-detail
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.headings :as h]
            [webapp.components.icon :as icon]
            [webapp.components.popover :as popover]
            [webapp.components.user-icon :as user-icon]
            [webapp.formatters :as formatters]))

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

(defn- review-groups [session]
  (let [user (rf/subscribe [:users->current-user])
        popover-open? (r/atom false)
        review (:review session)]
    (fn []
      (let [review-groups-data (:review_groups_data review)
            can-review? (and
                         (some #(= "PENDING" (:status %))
                               review-groups-data)
                         (some (fn [review-group]
                                 (some #(= (:group review-group) %)
                                       (-> @user :data :groups)))
                               review-groups-data))
            add-review-cb (fn [status]
                            (rf/dispatch [:reviews-plugin->add-review
                                          session
                                          status])
                            (reset! popover-open? false))]
        [:section
         {:class "flex flex-col gap-small"}
         (when can-review?
           [:div
            {:id "add-your-review-container"
             :class "relative flex justify-end mb-small"}
            [button/secondary {:outlined true
                               :text [:span
                                      {:class "flex items-center gap-small"}
                                      [:span "Add your review"]
                                      [icon/regular {:size 4
                                                     :icon-name "cheveron-down"}]]
                               :on-click #(reset! popover-open? (not @popover-open?))
                               :variant :small}]
            [popover/right {:open @popover-open?
                            :component [add-review-popover add-review-cb]
                            :on-click-outside #(reset! popover-open? false)}]])
         [:div
          {:class (str "flex flex-col gap-small justify-center"
                       " rounded-lg bg-gray-50 p-regular border")}
          (doall
           (for [group review-groups-data]
             ^{:key (:id group)}
             [:div
              {:class (str "flex items-center gap-small"
                           " text-sm")}
              [icon/regular {:size 4
                             :icon-name "user-group"}]
              [:span (:group group)]
              [:span
               {:class "flex-grow text-xs italic text-gray-500 text-right"}
               (:status group)]
              [icon/regular {:size 4
                             :icon-name (review-status-icon (:status group))}]]))]]))))

(defn review-details-page [session]
  (let [user-name (:user_name session)
        connection-name (:connection session)
        review (:review session)
        session-type (:type session)]
    [:div {:class "p-large bg-white rounded-lg"}
     [:header {:class "mb-large border-b pb-regular"}
      [h/h2 {:class "text-gray-800"} connection-name]]

     [:section
      {:id "review-info"
       :class "grid grid-cols-1 lg:grid-cols-3 gap-large items-center mb-large"}
      [:div {:class "col-span-1 flex gap-large items-center"}
       [:div {:class "flex flex-grow gap-regular items-center"}
        [user-icon/initials-black user-name]
        [:span
         {:class "text-gray-800 text-sm"}
         user-name]]]

      [:div {:class "text-sm col-span-1 flex flex-col gap-small"}
       [:div {:class "flex items-center gap-small"}
        [:span {:class "text-gray-500"}
         "started:"]
        [:span {:class "font-bold"}
         (formatters/time-parsed->full-date (:start_date session))]]
       (when (> (or (:access_duration review) 0) 0)
         [:div {:class "flex items-center gap-small"}
          [:span {:class "text-gray-500"}
           "session time:"]
          [:span {:class "font-bold"}
           (formatters/time-elapsed (/ (:access_duration review) 1000000))]])
       [:div {:class "flex items-center gap-small"}
        [:span {:class "text-gray-500"}
         "type:"]
        [:span {:class "font-bold"}
         session-type]]]
      [review-groups session]]

     (when (not (cs/blank? (-> session :script :data)))
       [:section
        {:id "review-command-area"
         :class "pt-large"}
        [:div
         {:class (str "rounded-lg p-regular bg-gray-800 text-white"
                      " whitespace-pre font-mono overflow-auto"
                      " text-sm text-gray-50 border border-gray-700")}
         [:div {:class "flex items-center gap-small mb-small"}
          [icon/regular {:icon-name "terminal"
                         :size 4
                         :class "text-gray-400"}]
          [:span {:class "text-gray-400"} "Command Input"]]
         [:div
          [:span
           {:class "text-white font-bold"}
           "$ "]
          [:span
           (-> session :script :data)]]]])]))

;; Mantendo o código legado para compatibilidade, podemos remover mais tarde
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

