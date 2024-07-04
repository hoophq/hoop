(ns webapp.events.reviews-plugin
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :reviews-plugin->get-reviews
 (fn
   [{:keys [db]} [_ limit]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri "/reviews"
                             :on-success #(rf/dispatch [:reviews-plugin->set-reviews %])}]]
         [:dispatch [:reviews-plugin->set-reviews-status :loading]]]}))

(rf/reg-event-fx
 :reviews-plugin->set-reviews
 (fn
   [{:keys [db]} [_ reviews]]
   {:fx [[:dispatch [:reviews-plugin->set-reviews-status :success]]]
    :db (assoc-in db [:reviews-plugin->reviews :results] reviews)}))

(rf/reg-event-fx
 :reviews-plugin->set-reviews-status
 (fn
   [{:keys [db]} [_ status]]
   {:db (assoc-in db [:reviews-plugin->reviews :status] status)}))

(rf/reg-event-fx
 :reviews-plugin->get-review-by-id
 (fn
   [{:keys [db]} [_ review]]
   (let [state {:status :loading
                :review review
                :review-logs {:status :loading}}]
     {:db (assoc db :reviews-plugin->review-details state)
      :fx [[:dispatch [:fetch
                       {:method "GET"
                        :uri (str "/reviews/" (:id review))
                        :on-success #(rf/dispatch
                                      [:reviews-plugin->set-review-detail %])}]]]})))

(rf/reg-event-fx
 :reviews-plugin->set-review-detail
 (declare db _ details)
 (fn
   [{:keys [db]} [_ details]]
   (let [cached-review (-> db :reviews-plugin->review-details :review)
         updated-review (merge cached-review details)]
     {:db (assoc db
                 :reviews-plugin->review-details
                 {:review updated-review
                  :status :opened})})))

(rf/reg-event-fx
 :reviews-plugin->add-review
 (fn
   [_ [_ review status]]
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
                        (rf/dispatch [:reviews-plugin->get-review-by-id review]))
                      500))}]]
         [:dispatch [:reviews-plugin->get-reviews]]]}))

