(ns webapp.resources.events
  (:require
   [re-frame.core :as rf]))

;; Cache configuration
(def cache-ttl-ms (* 120 60 1000)) ; 2 hours in milliseconds

;; Helper functions for cache management
(defn cache-valid? [db]
  (let [{:keys [cache-timestamp]} (:resources db)
        now (.now js/Date)]
    (and cache-timestamp
         (< (- now cache-timestamp) cache-ttl-ms))))

(defn get-cached-resources [db]
  (get-in db [:resources :results]))

;; Paginated resources events
(rf/reg-event-fx
 :resources/get-resources-paginated
 (fn
   [{:keys [db]} [_ {:keys [page-size page filters search force-refresh?]
                     :or {page-size 50 page 1 force-refresh? false}}]]
   (let [request {:page-size page-size
                  :page page
                  :filters filters
                  :search search
                  :force-refresh? force-refresh?}
         query-params (cond-> {}
                        page-size (assoc :page_size page-size)
                        page (assoc :page page)
                        search (assoc :search search)
                        (:tag_selector filters) (assoc :tag_selector (:tag_selector filters))
                        (:type filters) (assoc :type (:type filters))
                        (:subtype filters) (assoc :subtype (:subtype filters)))]
     {:db (-> db
              (update-in [:resources->pagination] merge
                         {:loading true
                          :page-size page-size
                          :current-page page
                          :active-filters filters
                          :active-search search}))
      :fx [[:dispatch
            [:fetch {:method "GET"
                     :uri "/resources"
                     :query-params query-params
                     :on-success #(rf/dispatch [:resources/set-resources-paginated (assoc request :response %)])}]]]})))

(rf/reg-event-fx
 :resources/set-resources-paginated
 (fn
   [{:keys [db]} [_ {:keys [response force-refresh?]}]]
   (let [resources-data response
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
            (assoc :resources->resource-details {:loading false :data resource})
            ;; Also store in details map for quick lookup
            (assoc-in [:resources :details (:id resource)] resource))
    :fx (cond-> []
          on-success (conj [:dispatch (conj on-success (:id resource))]))}))

(rf/reg-event-db
 :resources->clear-resource-details
 (fn [db [_]]
   (assoc db :resources->resource-details {:loading true :data nil})))

;; Load metadata
(rf/reg-event-fx
 :resources->load-metadata
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:resources :metadata :loading] true)
    :fx [[:dispatch [:http-request {:method "GET"
                                    :url "/data/resources-metadata.json"
                                    :on-success #(rf/dispatch [:resources->set-metadata %])
                                    :on-failure #(rf/dispatch [:resources->metadata-error %])}]]]}))

(rf/reg-event-db
 :resources->set-metadata
 (fn [db [_ metadata]]
   (assoc-in db [:resources :metadata] {:data metadata :loading false :error nil})))

(rf/reg-event-db
 :resources->metadata-error
 (fn [db [_ error]]
   (assoc-in db [:resources :metadata] {:data nil :loading false :error error})))

;; Create resource
(rf/reg-event-fx
 :resources->create-resource
 (fn
   [_ [_ resource]]
   (let [body (apply merge (for [[k v] resource :when (not (= "" v))] {k v}))]
     {:fx [[:dispatch [:fetch
                       {:method "POST"
                        :uri "/resources"
                        :body body
                        :on-success (fn [_response]
                                      (rf/dispatch [:modal->close])
                                      (rf/dispatch [:resources/get-resources-paginated {:force-refresh? true}])
                                      (rf/dispatch [:show-snackbar {:level :success
                                                                    :text "Resource created!"}])
                                      (rf/dispatch [:navigate :resources]))}]]]})))

;; Update resource
(rf/reg-event-fx
 :resources->update-resource
 (fn
   [_ [_ resource]]
   {:fx [[:dispatch [:fetch
                     {:method "PUT"
                      :uri (str "/resources/" (:id resource))
                      :body resource
                      :on-success (fn []
                                    (rf/dispatch [:modal->close])
                                    (rf/dispatch [:show-snackbar
                                                  {:level :success
                                                   :text (str "Resource " (:name resource) " updated!")}])
                                    (rf/dispatch [:resources/get-resources-paginated {:force-refresh? true}])
                                    (rf/dispatch [:navigate :resources]))}]]]}))

;; Delete resource
(rf/reg-event-fx
 :resources->delete-resource
 (fn
   [_ [_ resource-id]]
   {:fx [[:dispatch
          [:fetch {:method "DELETE"
                   :uri (str "/resources/" resource-id)
                   :on-success (fn []
                                 (rf/dispatch [:show-snackbar {:level :success
                                                               :text "Resource deleted!"}])
                                 (rf/dispatch [:resources/get-resources-paginated {:force-refresh? true}])
                                 (rf/dispatch [:navigate :resources]))}]]]}))

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

