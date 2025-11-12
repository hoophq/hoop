(ns webapp.resources.events
  (:require
   [re-frame.core :as rf]
   [webapp.connections.views.setup.events.process-form :as process-form]))

;; Paginated resources events
(rf/reg-event-fx
 :resources/get-resources-paginated
 (fn
   [{:keys [db]} [_ {:keys [page-size page filters name force-refresh?]
                     :or {page-size 50 page 1 force-refresh? false}}]]
   (let [request {:page-size page-size
                  :page page
                  :filters filters
                  :name name
                  :force-refresh? force-refresh?}
         query-params (cond-> {}
                        page-size (assoc :page_size page-size)
                        page (assoc :page page)
                        name (assoc :name name)
                        (:tag_selector filters) (assoc :tag_selector (:tag_selector filters))
                        (:type filters) (assoc :type (:type filters))
                        (:subtype filters) (assoc :subtype (:subtype filters)))]
     {:db (-> db
              (update-in [:resources->pagination] merge
                         {:loading true
                          :page-size page-size
                          :current-page page
                          :active-filters filters
                          :active-name name}))
      :fx [[:dispatch
            [:fetch {:method "GET"
                     :uri "/resources"
                     :query-params query-params
                     :on-success #(rf/dispatch [:resources/set-resources-paginated (assoc request :response %)])}]]]})))

(rf/reg-event-fx
 :resources/set-resources-paginated
 (fn
   [{:keys [db]} [_ {:keys [response force-refresh?]}]]
   (let [resources-data (get response :data [])
         pages-info (get response :pages {})
         page-number (get pages-info :page 1)
         page-size (get pages-info :size 50)
         total (get pages-info :total 0)
         existing-resources (get-in db [:resources->pagination :data] [])
         final-resources (if force-refresh? resources-data (vec (concat existing-resources resources-data)))
         has-more? (< (* page-number page-size) total)]
     {:db (-> db
              (update-in [:resources->pagination] merge
                         {:data final-resources
                          :loading false
                          :has-more? has-more?
                          :current-page page-number
                          :page-size page-size
                          :total total}))})))

;; Get resource details
(rf/reg-event-fx
 :resources->get-resource-details
 (fn
   [{:keys [db]} [_ resource-id & [on-success]]]
   {:db (-> db
            (assoc :resources->resource-details {:loading true :data {:id resource-id}}))
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/resources/" resource-id)
                   :on-success (fn [resource]
                                 (rf/dispatch [:resources->set-resource resource on-success]))}]]]}))

(rf/reg-event-fx
 :resources->set-resource
 (fn
   [{:keys [db]} [_ resource on-success]]
   {:db (-> db
            (assoc :resources->resource-details {:loading false :data resource}))
    :fx (cond-> []
          on-success (conj [:dispatch (conj on-success (:id resource))]))}))

;; Update role connection (with redirect to resource configure)
(rf/reg-event-fx
 :resources->update-role-connection
 (fn
   [{:keys [db]} [_ {:keys [name from-page resource-name]}]]
   (println "name" from-page)
   (let [body (process-form/process-payload db)]
     {:fx [[:dispatch [:fetch
                       {:method "PUT"
                        :uri (str "/connections/" name)
                        :body body
                        :on-success (fn []
                                      (rf/dispatch [:modal->close])
                                      (rf/dispatch [:show-snackbar
                                                    {:level :success
                                                     :text (str "Role " name " updated!")}])
                                      (rf/dispatch [:plugins->get-my-plugins])
                                      (rf/dispatch [:connections/get-connections-paginated {:force-refresh? true}])
                                      ;; Redirect back to resource configure using resource_name
                                      (if (= from-page "resource-configure")
                                        (rf/dispatch [:navigate :configure-resource {:tab "roles"} :resource-id resource-name])
                                        (rf/dispatch [:navigate :resources {:tab "roles"}])))}]]]})))

;; Get resource roles (connections) - Paginated
(rf/reg-event-fx
 :resources->get-resource-roles
 (fn
   [{:keys [db]} [_ resource-id {:keys [page-size page force-refresh?]
                                 :or {page-size 50 page 1 force-refresh? false}}]]
   (let [request {:page-size page-size
                  :page page
                  :resource-id resource-id
                  :force-refresh? force-refresh?}
         query-params {:resource_name resource-id
                       :page_size page-size
                       :page page}]
     {:db (-> db
              (update-in [:resources->resource-roles resource-id] merge
                         {:loading true
                          :page-size page-size
                          :current-page page}))
      :fx [[:dispatch
            [:fetch {:method "GET"
                     :uri "/connections"
                     :query-params query-params
                     :on-success (fn [response]
                                   (rf/dispatch [:resources->set-resource-roles resource-id (assoc request :response response)]))}]]]})))

(rf/reg-event-db
 :resources->set-resource-roles
 (fn [db [_ resource-id {:keys [response force-refresh?]}]]
   (let [connections-data (get response :data [])
         pages-info (get response :pages {})
         page-number (get pages-info :page 1)
         page-size (get pages-info :size 50)
         total (get pages-info :total 0)
         existing-roles (get-in db [:resources->resource-roles resource-id :data] [])
         final-roles (if force-refresh? connections-data (vec (concat existing-roles connections-data)))
         has-more? (< (* page-number page-size) total)]
     (assoc-in db [:resources->resource-roles resource-id]
               {:loading false
                :data final-roles
                :has-more? has-more?
                :current-page page-number
                :page-size page-size
                :total total}))))

;; Update resource name
(rf/reg-event-fx
 :resources->update-resource-name
 (fn
   [{:keys [db]} [_ old-resource-name new-resource-name]]
   (let [resource (get-in db [:resources->resource-details :data])
         body (clj->js {:name new-resource-name
                        :type (:type resource)
                        :subtype (:subtype resource)
                        :agent_id (:agent_id resource)
                        :env_vars (or (:env_vars resource) {})})]
     {:db (assoc-in db [:resources->resource-details :updating?] true)
      :fx [[:dispatch
            [:fetch {:method "PUT"
                     :uri (str "/resources/" old-resource-name)
                     :body body
                     :on-success (fn []
                                   (rf/dispatch [:resources->update-resource-name-success new-resource-name])
                                   (rf/dispatch [:show-snackbar
                                                 {:level :success
                                                  :text "Resource name updated successfully!"}])
                                   (rf/dispatch [:navigate :configure-resource {} :resource-id new-resource-name]))
                     :on-failure (fn [_error]
                                   (rf/dispatch [:resources->update-resource-name-failure])
                                   (rf/dispatch [:show-snackbar
                                                 {:level :error
                                                  :text "Failed to update resource name"}]))}]]]})))

(rf/reg-event-db
 :resources->update-resource-name-success
 (fn [db [_ new-name]]
   (-> db
       (assoc-in [:resources->resource-details :updating?] false)
       (assoc-in [:resources->resource-details :data :name] new-name))))

(rf/reg-event-db
 :resources->update-resource-name-failure
 (fn [db _]
   (assoc-in db [:resources->resource-details :updating?] false)))

