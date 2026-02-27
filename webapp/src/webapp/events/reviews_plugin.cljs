(ns webapp.events.reviews-plugin
  (:require
   [re-frame.core :as rf]))


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
