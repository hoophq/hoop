(ns webapp.events.connections
  (:require
   [clojure.edn :refer [read-string]]
   [re-frame.core :as rf]
   [webapp.connections.constants :as constants]
   [webapp.connections.views.setup.events.process-form :as process-form]))

;; Cache configuration
(def cache-ttl-ms (* 120 60 1000)) ; 2 hours in milliseconds

;; Helper functions for cache management
(defn cache-valid? [db]
  (let [{:keys [cache-timestamp]} (:connections db)
        now (.now js/Date)]
    (and cache-timestamp
         (< (- now cache-timestamp) cache-ttl-ms))))

(defn get-cached-connections [db]
  (get-in db [:connections :results]))

(rf/reg-event-fx
 :connections->get-connection-details
 (fn
   [{:keys [db]} [_ connection-name & [on-success]]]
   {:db (-> db
            (assoc :connections->connection-details {:loading true :data {:name connection-name}})
            (assoc-in [:connections :details] {}))
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri (str "/connections/" connection-name)
                   :on-success (fn [connection]
                                 (rf/dispatch [:connections->set-connection connection on-success]))}]]]}))

(rf/reg-event-fx
 :connections->set-connection
 (fn
   [{:keys [db]} [_ connection on-success]]
   {:db (-> db
            (assoc :connections->connection-details {:loading false :data connection})
            ;; Also store in details map for quick lookup
            (assoc-in [:connections :details (:name connection)] connection))
    ;; Check if this completes a batch loading and handle callback
    :fx (cond-> [[:dispatch [:connections->check-batch-complete connection]]]
          on-success (conj [:dispatch (conj on-success (:name connection))]))}))

;; Batch loader for multiple connections by name
(rf/reg-event-fx
 :connections->get-multiple-by-names
 (fn [{:keys [db]} [_ connection-names on-success on-failure]]
   (let [requests (mapv (fn [name]
                          [:dispatch [:connections->get-connection-details name]])
                        connection-names)]
     {:db (assoc db :connections->batch-loading
                 {:names (set connection-names)
                  :loaded #{}
                  :on-success on-success
                  :on-failure on-failure})
      :fx requests})))

;; Check if batch loading is complete
(rf/reg-event-fx
 :connections->check-batch-complete
 (fn [{:keys [db]} [_ connection]]
   (let [batch (get db :connections->batch-loading)
         loaded (conj (:loaded batch #{}) (:name connection))
         all-loaded? (= (:names batch) loaded)]
     (if (and batch all-loaded?)
       ;; All loaded - call success callback
       {:db (dissoc db :connections->batch-loading)
        :fx [[:dispatch (:on-success batch)]]}
       ;; Still waiting for more
       {:db (assoc-in db [:connections->batch-loading :loaded] loaded)}))))

(rf/reg-event-db
 :connections->clear-connection-details
 (fn [db [_]]
   (assoc db :connections->connection-details {:loading true :data nil})))

(rf/reg-event-fx
 :connections->get-connections
 (fn [{:keys [db]} [_ {:keys [filters on-success on-failure force-refresh?]
                       :or {on-success [:connections->set-connections]
                            on-failure [:connections->set-connections-error]}}]]

   (cond
     filters
     ;; If filters are provided, delegate to filter-connections
     {:fx [[:dispatch [:connections->filter-connections filters]]]}

     (and (not force-refresh?) (cache-valid? db))
     ;; Use cached data if valid
     (let [cached-connections (get-cached-connections db)]
       {:fx [[:dispatch (conj on-success cached-connections)]]})

     :else
     ;; Make fresh request
     {:db (assoc-in db [:connections :loading] true)
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri "/connections"
                               :on-success #(rf/dispatch [:connections->cache-and-notify % on-success])
                               :on-failure #(rf/dispatch (conj on-failure %))}]]]})))

;; New event to cache connections and notify callback
(rf/reg-event-fx
 :connections->cache-and-notify
 (fn [{:keys [db]} [_ connections on-success]]
   {:db (update db :connection merge {:results connections
                                      :loading false
                                      :cache-timestamp (.now js/Date)})
    :fx [[:dispatch (conj on-success connections)]]}))

(rf/reg-event-fx
 :connections->set-connections
 (fn
   [{:keys [db]} [_ connections]]
   {:db (update db :connections merge {:results connections
                                       :loading false
                                       :cache-timestamp (.now js/Date)})}))

;; Error event for connections
(rf/reg-event-fx
 :connections->set-connections-error
 (fn [{:keys [db]} [_ error]]
   {:db (update db :connections merge {:loading false
                                       :error error})}))

;; Paginated connections events
(rf/reg-event-fx
 :connections/get-connections-paginated
 (fn
   [{:keys [db]} [_ {:keys [page-size page filters search reset?]
                     :or {page-size 20 page 1 reset? true}}]]
   (let [request {:page-size page-size
                  :page page
                  :filters filters
                  :search search
                  :reset? reset?}
         query-params (cond-> {}
                        page-size (assoc :page_size page-size)
                        page (assoc :page page)
                        search (assoc :search search)
                        (:tag_selector filters) (assoc :tag_selector (:tag_selector filters))
                        (:type filters) (assoc :type (:type filters))
                        (:subtype filters) (assoc :subtype (:subtype filters)))]
     {:db (-> db
              (assoc-in [:connections->pagination :loading] true)
              (assoc-in [:connections->pagination :page-size] page-size)
              (assoc-in [:connections->pagination :current-page] page)
              (assoc-in [:connections->pagination :active-filters] filters)
              (assoc-in [:connections->pagination :active-search] search))
      :fx [[:dispatch
            [:fetch {:method "GET"
                     :uri "/connections"
                     :query-params query-params
                     :on-success #(rf/dispatch [:connections/set-connections-paginated (assoc request :response %)])}]]]})))

(rf/reg-event-fx
 :connections/set-connections-paginated
 (fn
   [{:keys [db]} [_ {:keys [response reset?]}]]
   (let [connections-data (get response :data [])
         pages-info (get response :pages {})
         page-number (get pages-info :page 1)
         page-size (get pages-info :size 20)
         total (get pages-info :total 0)
         existing-connections (get-in db [:connections->pagination :data] [])
         final-connections (if reset? connections-data (vec (concat existing-connections connections-data)))
         has-more? (< (* page-number page-size) total)]
     {:db (-> db
              (assoc-in [:connections->pagination :data] final-connections)
              (assoc-in [:connections->pagination :loading] false)
              (assoc-in [:connections->pagination :has-more?] has-more?)
              (assoc-in [:connections->pagination :current-page] page-number)
              (assoc-in [:connections->pagination :page-size] page-size)
              (assoc-in [:connections->pagination :total] total))})))


(rf/reg-event-fx
 :connections->load-metadata
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:connections :metadata :loading] true)
    :fx [[:dispatch [:http-request {:method "GET"
                                    :url "/data/connections-metadata.json"
                                    :on-success #(rf/dispatch [:connections->set-metadata %])
                                    :on-failure #(rf/dispatch [:connections->metadata-error %])}]]]}))

(rf/reg-event-db
 :connections->set-metadata
 (fn [db [_ metadata]]
   (assoc-in db [:connections :metadata] {:data metadata :loading false :error nil})))

(rf/reg-event-db
 :connections->metadata-error
 (fn [db [_ error]]
   (assoc-in db [:connections :metadata] {:data nil :loading false :error error})))

(rf/reg-event-fx
 :connections->create-connection
 (fn
   [{:keys [db]} [_ connection]]
   (let [body (apply merge (for [[k v] connection :when (not (= "" v))] {k v}))]
     {:fx [[:dispatch [:fetch
                       {:method "POST"
                        :uri "/connections"
                        :body body
                        :on-success (fn [connection]
                                      (rf/dispatch [:close-modal])
                                      ;; plugins might be updated in the connection
                                      ;; creation action, so we get them again here
                                      (rf/dispatch [:plugins->get-my-plugins])
                                      (rf/dispatch [:connections->get-connections {:force-refresh? true}])
                                      (rf/dispatch [:show-snackbar {:level :success
                                                                    :text "Connection created!"}])

                                      (rf/dispatch [:navigate :connections]))}]]]})))


(rf/reg-event-fx
 :connections->update-connection
 (fn
   [{:keys [db]} [_ connection]]
   (let [body (process-form/process-payload db)]
     {:fx [[:dispatch [:fetch
                       {:method "PUT"
                        :uri (str "/connections/" (:name connection))
                        :body body
                        :on-success (fn []
                                      (rf/dispatch [:modal->close])
                                      (rf/dispatch [:show-snackbar
                                                    {:level :success
                                                     :text (str "Connection " (:name connection) " updated!")}])
                                      (rf/dispatch [:plugins->get-my-plugins])
                                      (rf/dispatch [:connections->get-connections {:force-refresh? true}])
                                      (rf/dispatch [:navigate :connections]))}]]]})))

(rf/reg-event-fx
 :connections->test-connection
 (fn
   [{:keys [db]} [_ connection-name]]
   {:db (assoc db :connections->test-connection
               {:loading true
                :connection-name connection-name
                :agent-status :checking
                :connection-status :checking})
    :fx [;; Fetch connection details to check agent status
         [:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/connections/" connection-name)
                      :on-success (fn [response]
                                    (rf/dispatch [:connections->test-agent-status-success response connection-name]))
                      :on-failure (fn [error]
                                    (rf/dispatch [:connections->test-agent-status-error error connection-name]))}]]
         ;; Test connection endpoint
         [:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/connections/" connection-name "/test")
                      :on-success (fn [response]
                                    (rf/dispatch [:connections->test-connection-status-success response connection-name]))
                      :on-failure (fn [error]
                                    (rf/dispatch [:connections->test-connection-status-error error connection-name]))}]]]}))

(rf/reg-event-fx
 :connections->test-agent-status-success
 (fn
   [{:keys [db]} [_ response]]
   (let [agent-status (if (= (:status response) "online") :online :offline)
         current-test (get db :connections->test-connection)
         both-complete? (not= (:connection-status current-test) :checking)]
     {:db (assoc db :connections->test-connection
                 (assoc current-test
                        :agent-status agent-status
                        :loading (not both-complete?)))})))

(rf/reg-event-fx
 :connections->test-agent-status-error
 (fn
   [{:keys [db]} [_ _error]]
   (let [current-test (get db :connections->test-connection)
         both-complete? (not= (:connection-status current-test) :checking)]
     {:db (assoc db :connections->test-connection
                 (assoc current-test
                        :agent-status :offline
                        :loading (not both-complete?)))})))

(rf/reg-event-fx
 :connections->test-connection-status-success
 (fn
   [{:keys [db]} [_ _response]]
   (let [current-test (get db :connections->test-connection)
         both-complete? (not= (:agent-status current-test) :checking)]
     {:db (assoc db :connections->test-connection
                 (assoc current-test
                        :connection-status :successful
                        :loading (not both-complete?)))})))

(rf/reg-event-fx
 :connections->test-connection-status-error
 (fn
   [{:keys [db]} [_ _error]]
   (let [current-test (get db :connections->test-connection)
         both-complete? (not= (:agent-status current-test) :checking)]
     {:db (assoc db :connections->test-connection
                 (assoc current-test
                        :connection-status :failed
                        :loading (not both-complete?)))})))

(rf/reg-event-fx
 :connections->close-test-modal
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :connections->test-connection nil)}))

(rf/reg-event-fx
 ::connections->quickstart-create-connection
 (fn [{:keys [db]} [_ connection]]
   (let [body (apply merge (for [[k v] connection :when (not (= "" v))] {k v}))]
     {:fx [[:dispatch
            [:fetch
             {:method "POST"
              :uri "/connections"
              :body body
              :on-success (fn []
                            (rf/dispatch [:show-snackbar {:level :success
                                                          :text "Connection created!"}])
                            (rf/dispatch [:connections->get-connections {:force-refresh? true}])
                            (rf/dispatch [:plugins->get-my-plugins])
                            (rf/dispatch [:navigate :home]))}]]]})))

(def quickstart-query
  "SELECT c.firstname, c.lastname, o.orderid, o.orderdate, SUM(ol.quantity) AS total_quantity, SUM(ol.quantity * p.price) AS total_amount
FROM customers c
JOIN orders o ON c.customerid = o.customerid
JOIN orderlines ol ON o.orderid = ol.orderid
JOIN products p ON ol.prod_id = p.prod_id
WHERE c.country = 'US'
GROUP BY c.firstname, c.lastname, o.orderid, o.orderdate
ORDER BY total_amount DESC;")

(rf/reg-event-fx
 :connections->quickstart-create-postgres-demo
 (fn [{:keys [db]} [_]]
   (let [agents (get-in db [:agents :data])
         agent (first agents)]
     (if agent
       ;; If agent exists in app state, use it directly
       (let [connection (merge constants/connection-postgres-demo
                               {:agent_id (:id agent)})
             code-tmp-db {:date (.now js/Date)
                          :code quickstart-query}
             code-tmp-db-json (.stringify js/JSON (clj->js code-tmp-db))]
         (.setItem js/localStorage :code-tmp-db code-tmp-db-json)
         {:fx [[:dispatch [::connections->quickstart-create-connection connection]]]})

       ;; If no agent in app state, navigate back to setup to start agent check
       {:fx [[:dispatch [:navigate :onboarding]]
             [:dispatch [:show-snackbar {:level :error
                                         :text "Setting up agents before creating demo database..."}]]]}))))

(rf/reg-event-fx
 :connections->delete-connection
 (fn
   [{:keys [db]} [_ connection-name]]
   {:fx [[:dispatch
          [:fetch {:method "DELETE"
                   :uri (str "/connections/" connection-name)
                   :on-success (fn []
                                 (let [localstorage-connection
                                       (first
                                        (read-string (.getItem js/localStorage "run-connection-list-selected")))]

                                   (when (= connection-name (:name localstorage-connection))
                                     (.removeItem js/localStorage "run-connection-list-selected"))

                                   (rf/dispatch [:show-snackbar {:level :success
                                                                 :text "Connection deleted!"}])
                                   (rf/dispatch [:connections->get-connections {:force-refresh? true}])
                                   (rf/dispatch [:navigate :connections])))}]]]}))
