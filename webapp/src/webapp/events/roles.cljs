(ns webapp.events.roles
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :roles/get-roles-paginated
 (fn
   [{:keys [db]} [_ {:keys [page page-size filters reset?]
                     :or {page 1 page-size 10 reset? true}}]]
   (let [request {:page page
                  :page-size page-size
                  :filters filters
                  :reset? reset?}]
     {:db (-> db
              (assoc-in [:roles :loading] true)
              (assoc-in [:roles :current-page] page)
              (assoc-in [:roles :page-size] page-size)
              (assoc-in [:roles :active-filters] filters))
      :fx [[:dispatch
            [:fetch {:method "GET"
                     :uri "/connections"    ;; TODO: Update endpoint when available
                     :on-success #(rf/dispatch [:roles/set-roles-paginated (assoc request :response %)])
                     :on-failure #(rf/dispatch [:roles/set-roles-error %])}]]]})))

(rf/reg-event-fx
 :roles/set-roles-paginated
 (fn
  [{:keys [db]} [_ {:keys [response]}]]
   (let [all-roles (if (vector? response) response (:results response []))
         page-roles (take 15 all-roles)     ;; TODO: Remove when backend pagination is available
         existing-roles (get-in db [:roles :results] [])
         final-roles (vec (concat existing-roles page-roles))
         total-count (count all-roles)
         has-more? (< (count final-roles) total-count)]
     {:db (-> db
              (assoc-in [:roles :results] final-roles)
              (assoc-in [:roles :loading] false)
              (assoc-in [:roles :has-more?] has-more?)
              (assoc-in [:roles :total-count] total-count)
              (assoc-in [:roles :last-response-size] (count page-roles)))})))
