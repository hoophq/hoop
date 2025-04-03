(ns webapp.events.reviews-plugin
  (:require
   [re-frame.core :as rf]
   [webapp.reviews.review-detail :as review-detail]))

(rf/reg-event-fx
 :reviews-plugin->get-reviews
 (fn
   [{:keys [db]} [_ params]]
   (let [user (-> db :users->current-user :data)
         limit (or (:limit params) 20)
         status (:status params)
         base-uri (str "/sessions?review.approver=" (:email user) "&limit=" limit)
         uri (if (and status (not= status ""))
               (str base-uri "&review.status=" status)
               base-uri)]
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri uri
                               :on-success #(rf/dispatch [:reviews-plugin->set-reviews % params])}]]
           [:dispatch [:reviews-plugin->set-reviews-status :loading]]]})))

(rf/reg-event-fx
 :reviews-plugin->load-more-reviews
 (fn
   [{:keys [db]} _]
   (let [current-limit (or (-> db :reviews-plugin->reviews :params-limit) 20)
         current-status (or (-> db :reviews-plugin->reviews :params-status) "")
         new-limit (+ current-limit 20)
         params {:limit new-limit
                 :status current-status}]
     {:fx [[:dispatch [:reviews-plugin->get-reviews params]]]})))

(rf/reg-event-fx
 :reviews-plugin->set-reviews
 (fn
   [{:keys [db]} [_ sessions params]]
   (let [limit (or (:limit params) 20)
         status (or (:status params) "")]
     {:fx [[:dispatch [:reviews-plugin->set-reviews-status :success]]]
      :db (-> db
              (assoc-in [:reviews-plugin->reviews :results] (:data sessions))
              (assoc-in [:reviews-plugin->reviews :has_next_page] (:has_next_page sessions))
              (assoc-in [:reviews-plugin->reviews :total] (:total sessions))
              (assoc-in [:reviews-plugin->reviews :params-limit] limit)
              (assoc-in [:reviews-plugin->reviews :params-status] status))})))

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
   [{:keys [db]} [_ session status]]
   (let [review (:review session)
         current-status (get-in db [:reviews-plugin->reviews :params-status] "")
         current-limit (get-in db [:reviews-plugin->reviews :params-limit] 20)]
     {:fx [[:dispatch
            [:fetch {:method "PUT"
                     :uri (str "/reviews/" (:id review))
                     :body {:status status}
                     :on-success
                     (fn []
                       (rf/dispatch [:show-snackbar
                                     {:level :success
                                      :text "Your review was added"}])
                       (js/setTimeout
                        (fn []
                          (rf/dispatch [:reviews-plugin->get-reviews {:status current-status :limit current-limit}])
                          (rf/dispatch [:reviews-plugin->get-review-by-id session]))
                        500))}]]]})))

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
 :reviews-plugin->get-review-details
 (fn
   [{:keys [db]} [_ review-id]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/reviews/" review-id)
                             :on-success #(rf/dispatch [:reviews-plugin->handle-review-details %])}]]]}))

(rf/reg-event-fx
 :reviews-plugin->handle-review-details
 (fn
   [{:keys [db]} [_ review]]
   (let [session-id (:session review)]
     {:fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions/" session-id)
                               :on-success #(rf/dispatch [:reviews-plugin->set-review-detail %])}]]]})))

