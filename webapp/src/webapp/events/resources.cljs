(ns webapp.events.resources
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :resources->create-resource
 (fn [{:keys [db]} [_ payload]]
   (js/console.log "🚀 Creating resource with payload:" (clj->js payload))
   {:db (assoc-in db [:resources :creating?] true)
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/resources"
                             :body payload
                             :on-success (fn [response]
                                           (rf/dispatch [:resources->create-success response]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:resources->create-failure error]))}]]]}))

(rf/reg-event-fx
 :resources->create-success
 (fn [{:keys [db]} [_ resource]]
   (js/console.log "✅ Resource created successfully:" (clj->js resource))
   {:db (-> db
            (assoc-in [:resources :creating?] false)
            (assoc-in [:resources :last-created] resource))
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Resource created successfully!"}]]
         [:dispatch [:resource-setup->next-step :success]]]}))

(rf/reg-event-fx
 :resources->create-failure
 (fn [{:keys [db]} [_ error]]
   (js/console.error "❌ Failed to create resource:" error)
   {:db (assoc-in db [:resources :creating?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to create resource"
                                     :details (or (:message error) "Unknown error")}]]]}))
