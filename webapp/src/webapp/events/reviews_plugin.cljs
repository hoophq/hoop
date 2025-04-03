(ns webapp.events.reviews-plugin
  (:require
   [re-frame.core :as rf]
   [webapp.reviews.review-detail :as review-detail]))

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
   (let [state {:status :loading
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

(rf/reg-event-fx
 :reviews-plugin->get-session-details
 (fn
   [{:keys [db]} [_ session-id]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/sessions/" session-id)
                             :on-success #(rf/dispatch [:reviews-plugin->open-session-details %])}]]
         [:dispatch [:modal->set-modal-loading true]]]}))

(rf/reg-event-fx
 :reviews-plugin->open-session-details
 (fn
   [{:keys [db]} [_ session]]
   {:fx [[:dispatch [:modal->set-modal-loading false]]
         [:dispatch [:open-modal
                     [review-detail/review-details-page session] :large]]]}))

(rf/reg-event-fx
 :reviews-plugin->kill-session
 (fn
   [{:keys [db]} [_ session killing-status]]
   {:fx [[:dispatch [:fetch {:method "POST"
                             :uri (str "/sessions/" (:id session) "/kill")
                             :on-success (fn [_]
                                           (when killing-status
                                             (reset! killing-status :ready))
                                           (rf/dispatch [:show-snackbar
                                                         {:level :success
                                                          :text "Session killed successfully"}])
                                           (rf/dispatch [:reviews-plugin->get-reviews])
                                           (rf/dispatch [:modal->close]))
                             :on-failure (fn [error]
                                           (when killing-status
                                             (reset! killing-status :ready))
                                           (rf/dispatch [:show-snackbar
                                                         {:level :error
                                                          :text (or (:message error) "Failed to kill session")}]))}]]]}))

