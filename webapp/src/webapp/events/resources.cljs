(ns webapp.events.resources
  (:require
   [re-frame.core :as rf]))

;; Mock data for development
(def mock-resource-response
  {:id "resource-123"
   :name "MongoDB Customers"
   :type "database"
   :subtype "mongodb"
   :agent_id "agent-456"
   :roles [{:id "role-1"
            :name "mongodb-prod"
            :type "database"
            :subtype "mongodb"}
           {:id "role-2"
            :name "mongodb-readonly"
            :type "database"
            :subtype "mongodb"}]})

(rf/reg-event-fx
 :resources->create-resource
 (fn
   [{:keys [db]} [_ payload]]
   (js/console.log "🚀 Creating resource with payload:" (clj->js payload))

   ;; Mock API call - replace with real API later
   (js/setTimeout
    (fn []
      (rf/dispatch [:resources->create-success mock-resource-response]))
    1000)

   {:db (assoc-in db [:resources :creating?] true)}))

(rf/reg-event-fx
 :resources->create-success
 (fn
   [{:keys [db]} [_ resource]]
   (js/console.log "✅ Resource created successfully:" (clj->js resource))
   {:db (-> db
            (assoc-in [:resources :creating?] false)
            (assoc-in [:resources :last-created] resource))
    :fx [[:dispatch [:show-snackbar {:level :success
                                     :text "Resource created successfully!"}]]
         [:dispatch [:navigate :connections]]]}))

(rf/reg-event-fx
 :resources->create-failure
 (fn
   [{:keys [db]} [_ error]]
   (js/console.error "❌ Failed to create resource:" error)
   {:db (assoc-in db [:resources :creating?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to create resource"
                                     :details error}]]]}))

;; Get resources list (mock for now)
(rf/reg-event-fx
 :resources->get-resources
 (fn
   [{:keys [db]} [_]]
   ;; Mock - will be replaced with real API
   {:db (assoc-in db [:resources :loading] true)}))
