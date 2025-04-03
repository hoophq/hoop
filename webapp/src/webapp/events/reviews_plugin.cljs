(ns webapp.events.reviews-plugin
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :reviews-plugin->get-reviews
 (fn
   [{:keys [db]} [_ limit]]
   (let [user (-> db :users->current-user :data)]
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions?review.approver=" (:email user))
                               :on-success #(rf/dispatch [:reviews-plugin->set-reviews %])}]]
           [:dispatch [:reviews-plugin->set-reviews-status :loading]]]})))

(rf/reg-event-fx
 :reviews-plugin->set-reviews
 (fn
   [{:keys [db]} [_ sessions]]
   {:fx [[:dispatch [:reviews-plugin->set-reviews-status :success]]]
    :db (assoc-in db [:reviews-plugin->reviews :results] (:data sessions))}))

(rf/reg-event-fx
 :reviews-plugin->set-reviews-status
 (fn
   [{:keys [db]} [_ status]]
   {:db (assoc-in db [:reviews-plugin->reviews :status] status)}))

(rf/reg-event-fx
 :reviews-plugin->get-review-by-id
 (fn
   [{:keys [db]} [_ session]]
   (let [review (:review session)
         state {:status :loading
                :review session
                :review-logs {:status :loading}}]
     {:db (assoc db :reviews-plugin->review-details state)
      :fx [[:dispatch [:fetch
                       {:method "GET"
                        :uri (str "/sessions/" (:id session))
                        :on-success #(rf/dispatch
                                      [:reviews-plugin->set-review-detail %])}]]]})))

(rf/reg-event-fx
 :reviews-plugin->set-review-detail
 (fn
   [{:keys [db]} [_ session-details]]
   (let [cached-session (-> db :reviews-plugin->review-details :review)
         updated-session (merge cached-session session-details)]
     {:db (assoc db
                 :reviews-plugin->review-details
                 {:review updated-session
                  :status :opened})})))

(rf/reg-event-fx
 :reviews-plugin->add-review
 (fn
   [_ [_ session status]]
   (let [review (:review session)]
     {:fx [[:dispatch
            [:fetch {:method "PUT"
                     :uri (str "/reviews/" (:id review))
                     :body {:status status}
                     :on-success
                     (fn []
                       (rf/dispatch [:show-snackbar
                                     {:level :success
                                      :text (str "Your review was added")}])
                       (js/setTimeout
                        (fn []
                          (rf/dispatch [:reviews-plugin->get-reviews])
                          (rf/dispatch [:reviews-plugin->get-review-by-id session]))
                        500))}]]
           [:dispatch [:reviews-plugin->get-reviews]]]})))

