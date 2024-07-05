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

(defn- review-groups []
  (let [user (rf/subscribe [:users->current-user])
        popover-open? (r/atom false)]
    (fn [review]
      (let [can-review? (and
                         (some #(= "PENDING" (:status %))
                               (:review_groups_data review))
                         (some (fn [review-group]
                                 (some #(= (:group review-group) %)
                                       (-> @user :data :groups)))
                               (:review_groups_data review)))
            add-review-cb (fn [status]
                            (rf/dispatch [:reviews-plugin->add-review
                                          review
                                          status])
                            (reset! popover-open? false))]
        [:section
         {:class "flex flex-col gap-small"}
         (when can-review?
           [:div
            {:id "add-your-review-container"
             :class "relative flex justify-end"}
            [button/secondary {:outlined true
                               :text [:span
                                      {:class "flex items-center"}
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
                       " rounded-lg bg-gray-100 p-regular")}
          (doall
           (for [group (:review_groups_data review)]
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

(defmulti item-view identity)
(defmethod item-view :opened [_ review-details]
  (let [review (:review review-details)
        user-name (-> review :review_owner :name)]
    [:div
     [:header
      [h/h2 (-> review :review_connection :name)]]

     [:section
      {:id "review-info"
       :class "grid grid-cols-1 lg:grid-cols-3 gap-regular items-center"}
      [:div {:class "col-span-1 flex gap-large items-center"}
       [:div {:class "flex flex-grow gap-regular items-center"}
        [user-icon/initials-black user-name]
        [:span
         {:class "text-gray-800 text-sm"}
         user-name]]]

      [:div {:class "text-sm col-span-1 flex flex-col gap-small"}
       ;; TODO: Change to type (when (= (:type review) "jit")) when it was more secure to do.
       [:div {:class "flex items-center gap-small"}
        [:span {:class "text-gray-500"}
         "created:"]
        [:span {:class "font-bold"}
         (formatters/time-parsed->full-date (:created_at review))]]
       (when (> (:access_duration review) 0)
         [:div {:class "flex items-center gap-small"}
          [:span {:class "text-gray-500"}
           "session time:"]
          [:span {:class "font-bold"}
           (formatters/time-elapsed (/ (:access_duration review) 1000000))]])]
      [review-groups review]]
     ;; TODO: Change to type (when (= (:type review) "onetime")) when it was more secure to do.
     (when (not (cs/blank? (:input review)))
       [:section
        {:id "review-command-area"
         :class "pt-large"}
        [:div
         {:class (str "rounded-lg p-regular bg-gray-800 text-white"
                      " whitespace-pre font-mono overflow-auto"
                      " text-sm text-gray-50")}
         [:span
          {:class "text-white font-bold"}
          "$ "]
         [:span
          (:input review)]]])]))

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

(defn review-details-page []
  [:div
   {:class (str "bg-white p-large rounded-lg h-full")}
   [review-detail]])

