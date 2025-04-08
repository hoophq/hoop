(ns webapp.events.reviews-plugin
  (:require
   [re-frame.core :as rf]
   [webapp.reviews.review-detail :as review-detail]))

(rf/reg-event-fx
 :reviews-plugin->get-reviews
 (fn
   [{:keys [db]} [_ params]]
   (let [current-user (-> db :users->current-user :data :email)
         limit (or (:limit params) 20)
         status (:status params)
         connection (:connection params)
         user (:user params)
         start-date (:start_date params)
         end-date (:end_date params)
         base-uri (str "/sessions?review.approver=" (or user current-user) "&limit=" limit)
         uri-with-status (if (and status (not= status ""))
                           (str base-uri "&review.status=" status)
                           base-uri)
         uri-with-connection (if (and connection (not= connection ""))
                               (str uri-with-status "&connection=" connection)
                               uri-with-status)
         uri-with-start-date (if (and start-date (not= start-date ""))
                               (str uri-with-connection "&start_date=" start-date)
                               uri-with-connection)
         uri (if (and end-date (not= end-date ""))
               (str uri-with-start-date "&end_date=" end-date)
               uri-with-start-date)]
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
         current-user (or (-> db :reviews-plugin->reviews :params-user) "")
         current-connection (or (-> db :reviews-plugin->reviews :params-connection) "")
         current-start-date (or (-> db :reviews-plugin->reviews :params-start_date) "")
         current-end-date (or (-> db :reviews-plugin->reviews :params-end_date) "")
         new-limit (+ current-limit 20)
         params {:limit new-limit
                 :status current-status
                 :user current-user
                 :connection current-connection
                 :start_date current-start-date
                 :end_date current-end-date}]
     {:fx [[:dispatch [:reviews-plugin->get-reviews params]]]})))

(rf/reg-event-fx
 :reviews-plugin->set-reviews
 (fn
   [{:keys [db]} [_ sessions params]]
   (let [limit (or (:limit params) 20)
         status (or (:status params) "")
         user (or (:user params) "")
         connection (or (:connection params) "")
         start-date (or (:start_date params) "")
         end-date (or (:end_date params) "")]
     {:fx [[:dispatch [:reviews-plugin->set-reviews-status :success]]]
      :db (-> db
              (assoc-in [:reviews-plugin->reviews :results] (:data sessions))
              (assoc-in [:reviews-plugin->reviews :has_next_page] (:has_next_page sessions))
              (assoc-in [:reviews-plugin->reviews :total] (:total sessions))
              (assoc-in [:reviews-plugin->reviews :params-limit] limit)
              (assoc-in [:reviews-plugin->reviews :params-status] status)
              (assoc-in [:reviews-plugin->reviews :params-connection] connection)
              (assoc-in [:reviews-plugin->reviews :params-user] user)
              (assoc-in [:reviews-plugin->reviews :params-start_date] start-date)
              (assoc-in [:reviews-plugin->reviews :params-end_date] end-date))})))

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
         current-limit (get-in db [:reviews-plugin->reviews :params-limit] 20)
         current-user (get-in db [:reviews-plugin->reviews :params-user] "")
         current-connection (get-in db [:reviews-plugin->reviews :params-connection] "")
         current-start-date (get-in db [:reviews-plugin->reviews :params-start_date] "")
         current-end-date (get-in db [:reviews-plugin->reviews :params-end_date] "")]
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
                          (rf/dispatch [:reviews-plugin->get-reviews {:status current-status
                                                                      :user current-user
                                                                      :limit current-limit
                                                                      :connection current-connection
                                                                      :start_date current-start-date
                                                                      :end_date current-end-date}])
                          (rf/dispatch [:reviews-plugin->get-review-by-id session]))
                        500))}]]
           [:dispatch [:reviews-plugin->get-reviews {:status current-status
                                                     :user current-user
                                                     :limit current-limit
                                                     :connection current-connection
                                                     :start_date current-start-date
                                                     :end_date current-end-date}]]]})))

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
