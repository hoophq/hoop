(ns webapp.resources.setup.guardrails-suggestions.events
  (:require
   [re-frame.core :as rf]
   [webapp.events.guardrails :as gr-events]))

;; State path: [:resource-setup :guardrails-suggestions]
;;
;; Shape:
;; {:selected-toggles {<suggestion-name> #{<connection-id> ...}}
;;  :pending          #{<suggestion-name> ...}
;;  :existing         {<suggestion-name> <guardrail-id>}}

(def ^:private state-path [:resource-setup :guardrails-suggestions])

(defn- sanitize [guardrail]
  (-> guardrail
      (update :input #(gr-events/process-rules (:rules %)))
      (update :output #(gr-events/process-rules (:rules %)))))

(defn- guardrails-list [db]
  (get-in db [:guardrails->list :data] []))

(defn- find-by-name [db guardrail-name]
  (->> (guardrails-list db)
       (some #(when (= (:name %) guardrail-name) %))))

(defn- existing-map [db]
  (->> (guardrails-list db)
       (reduce (fn [acc g] (assoc acc (:name g) (:id g))) {})))

(defn- selected-toggles-from-list [db]
  (->> (guardrails-list db)
       (reduce (fn [acc g] (assoc acc (:name g) (set (:connection_ids g)))) {})))

(defn- original-ids-from-list [db]
  (->> (guardrails-list db)
       (reduce (fn [acc g] (assoc acc (:id g) (set (:connection_ids g)))) {})))

(rf/reg-event-fx
 :guardrails-suggestions/refetch
 (fn [_ _]
   {:fx [[:dispatch
          [:fetch
           {:method "GET"
            :uri "/guardrails"
            :on-success (fn [list]
                          (rf/dispatch [:guardrails->set-all list])
                          (rf/dispatch [:guardrails-suggestions/hydrate]))
            :on-failure (fn [error]
                          (rf/dispatch [:guardrails->set-all nil error]))}]]]}))

(rf/reg-event-fx
 :guardrails-suggestions/init
 (fn [{:keys [db]} _]
   (let [processed (get-in db [:resource-setup :processed-roles] [])
         last-created (get-in db [:resources :last-created-roles] [])
         role-names (->> (concat processed last-created)
                         (keep :name)
                         distinct
                         vec)]
     {:db (assoc-in db state-path {:selected-toggles {}
                                   :pending #{}
                                   :existing {}
                                   :roles []
                                   :open-items #{}
                                   :original-ids {}
                                   :new-selected {}
                                   :initial-guardrails nil})
      :fx (cond-> [[:dispatch [:guardrails-suggestions/refetch]]]
            (seq role-names)
            (conj [:dispatch [:guardrails-suggestions/fetch-roles role-names]]))})))

(rf/reg-event-fx
 :guardrails-suggestions/fetch-roles
 (fn [_ [_ role-names]]
   {:fx (mapv (fn [name]
                [:dispatch
                 [:fetch
                  {:method "GET"
                   :uri (str "/connections?name=" (js/encodeURIComponent name)
                             "&page=1&page_size=1")
                   :on-success (fn [response]
                                 (let [conn (or (-> response :data first)
                                                (when (vector? response) (first response)))]
                                   (when (and conn (:id conn))
                                     (rf/dispatch [:guardrails-suggestions/add-role
                                                   {:id (:id conn) :name (:name conn)}]))))
                   :on-failure (fn [error]
                                 (rf/dispatch [:show-snackbar {:level :error :text (str "Failed to fetch role: " name)}]))}]])
              role-names)}))

(rf/reg-event-db
 :guardrails-suggestions/add-role
 (fn [db [_ role]]
   (let [existing (get-in db (conj state-path :roles) [])
         already? (some #(= (:id %) (:id role)) existing)]
     (if already?
       db
       (update-in db (conj state-path :roles) (fnil conj []) role)))))

(rf/reg-event-db
 :guardrails-suggestions/hydrate
 (fn [db _]
   (let [already-captured? (some? (get-in db (conj state-path :initial-guardrails)))]
     (cond-> db
       true (assoc-in (conj state-path :existing) (existing-map db))
       true (update-in (conj state-path :selected-toggles)
                       #(merge (selected-toggles-from-list db) %))
       ;; Snapshot connection_ids per guardrail. Existing entries are kept
       ;; as first-load values; only new guardrails (e.g. ones just created
       ;; via a suggestion checkbox) get a fresh snapshot. This prevents a
       ;; later PUT from clobbering connection_ids the user just added.
       true (update-in (conj state-path :original-ids)
                       #(merge (original-ids-from-list db) %))
       ;; Snapshot of the full guardrails list captured ONCE on the first
       ;; hydrate of this flow. Drives :list-for-subtype and :your-guardrails
       ;; so that creating/deleting a guardrail mid-flow does not move
       ;; suggestions across sections. The snapshot refreshes on the next
       ;; resource setup flow because :init resets it to nil.
       (not already-captured?)
       (assoc-in (conj state-path :initial-guardrails) (vec (guardrails-list db)))))))

(rf/reg-event-db
 :guardrails-suggestions/mark-pending
 (fn [db [_ suggestion-name on?]]
   (update-in db (conj state-path :pending)
              (fnil (if on? conj disj) #{}) suggestion-name)))

(rf/reg-event-db
 :guardrails-suggestions/store-existing
 (fn [db [_ suggestion-name id]]
   (if id
     (assoc-in db (conj state-path :existing suggestion-name) id)
     (update-in db (conj state-path :existing) dissoc suggestion-name))))

;; Create a guardrail from a suggestion with the given connection_ids.
;; No-op when offline; success/failure update local state without navigating.
(rf/reg-event-fx
 :guardrails-suggestions/create
 (fn [_ [_ suggestion conn-ids]]
   (let [body (-> suggestion
                  (select-keys [:name :description :connection_ids :attributes :input :output])
                  (assoc :id "" :connection_ids (vec conn-ids))
                  sanitize)
         sname (:name suggestion)]
     {:fx [[:dispatch [:guardrails-suggestions/mark-pending sname true]]
           [:dispatch
            [:fetch
             {:method "POST"
              :uri "/guardrails"
              :body body
              :on-success (fn [response]
                            (rf/dispatch [:guardrails-suggestions/mark-pending sname false])
                            (rf/dispatch [:guardrails-suggestions/store-existing sname (:id response)])
                            (rf/dispatch [:guardrails-suggestions/refetch]))
              :on-failure (fn [error]
                            (rf/dispatch [:guardrails-suggestions/mark-pending sname false])
                            (rf/dispatch [:show-snackbar
                                          {:level :error
                                           :text (or (:message error) (str error))}]))}]]]})))

(rf/reg-event-fx
 :guardrails-suggestions/update
 (fn [{:keys [db]} [_ suggestion-name conn-ids]]
   (let [existing (find-by-name db suggestion-name)
         id (:id existing)]
     (if-not id
       {}
       (let [body (-> existing
                      (assoc :connection_ids (vec conn-ids))
                      sanitize)]
         {:fx [[:dispatch [:guardrails-suggestions/mark-pending suggestion-name true]]
               [:dispatch
                [:fetch
                 {:method "PUT"
                  :uri (str "/guardrails/" id)
                  :body body
                  :on-success (fn [_]
                                (rf/dispatch [:guardrails-suggestions/mark-pending suggestion-name false])
                                (rf/dispatch [:guardrails-suggestions/refetch]))
                  :on-failure (fn [error]
                                (rf/dispatch [:guardrails-suggestions/mark-pending suggestion-name false])
                                (rf/dispatch [:show-snackbar
                                              {:level :error
                                               :text (or (:message error) (str error))}]))}]]]})))))

(rf/reg-event-fx
 :guardrails-suggestions/delete
 (fn [{:keys [db]} [_ suggestion-name]]
   (let [id (get-in db (conj state-path :existing suggestion-name))]
     (if-not id
       {}
       {:fx [[:dispatch [:guardrails-suggestions/mark-pending suggestion-name true]]
             [:dispatch
              [:fetch
               {:method "DELETE"
                :uri (str "/guardrails/" id)
                :on-success (fn [_]
                              (rf/dispatch [:guardrails-suggestions/mark-pending suggestion-name false])
                              (rf/dispatch [:guardrails-suggestions/store-existing suggestion-name nil])
                              (rf/dispatch [:guardrails-suggestions/clear-toggles suggestion-name])
                              (rf/dispatch [:guardrails-suggestions/refetch]))
                :on-failure (fn [error]
                              (rf/dispatch [:guardrails-suggestions/mark-pending suggestion-name false])
                              (rf/dispatch [:show-snackbar
                                            {:level :error
                                             :text (or (:message error) (str error))}]))}]]]}))))

(rf/reg-event-db
 :guardrails-suggestions/clear-toggles
 (fn [db [_ suggestion-name]]
   (update-in db (conj state-path :selected-toggles) dissoc suggestion-name)))

(rf/reg-event-db
 :guardrails-suggestions/set-open-items
 (fn [db [_ names]]
   (assoc-in db (conj state-path :open-items) (set names))))

(rf/reg-event-db
 :guardrails-suggestions/open-item
 (fn [db [_ name]]
   (update-in db (conj state-path :open-items) (fnil conj #{}) name)))

;; Higher-level handlers wired to UI events.

;; Toggle a single role (Switch). Side effects depend on whether the
;; suggestion already exists on the server:
;; - existing + last toggle going off  -> DELETE
;; - existing                          -> PUT with new conn-ids
;; - not existing + first toggle on    -> POST (auto-checks the checkbox)
;; - not existing + toggle off only    -> local state only
(rf/reg-event-fx
 :guardrails-suggestions/toggle-role
 (fn [{:keys [db]} [_ suggestion conn-id on?]]
   (let [sname (:name suggestion)
         current (get-in db (conj state-path :selected-toggles sname) #{})
         next-set (if on? (conj current conn-id) (disj current conn-id))
         existing-id (get-in db (conj state-path :existing sname))]
     {:db (assoc-in db (conj state-path :selected-toggles sname) next-set)
      :fx (cond
            (and existing-id (empty? next-set))
            [[:dispatch [:guardrails-suggestions/delete sname]]]

            existing-id
            [[:dispatch [:guardrails-suggestions/update sname next-set]]]

            (and (not existing-id) (seq next-set))
            [[:dispatch [:guardrails-suggestions/create suggestion next-set]]]

            :else
            [])})))

;; ---------------------------------------------------------------------------
;; "Your Guardrails" — additive role logic. Never deletes nor removes
;; original connection_ids. Each toggle/checkbox PUTs with conn_ids =
;; (original ∪ user-selected new role ids).
;; ---------------------------------------------------------------------------

(rf/reg-event-fx
 :guardrails-suggestions/put-existing
 (fn [{:keys [db]} [_ guardrail conn-ids]]
   (let [id (:id guardrail)
         body (-> guardrail
                  (assoc :connection_ids (vec conn-ids))
                  sanitize)]
     {:fx [[:dispatch [:guardrails-suggestions/mark-pending id true]]
           [:dispatch
            [:fetch
             {:method "PUT"
              :uri (str "/guardrails/" id)
              :body body
              :on-success (fn [_]
                            (rf/dispatch [:guardrails-suggestions/mark-pending id false])
                            (rf/dispatch [:guardrails-suggestions/refetch]))
              :on-failure (fn [error]
                            (rf/dispatch [:guardrails-suggestions/mark-pending id false])
                            (rf/dispatch [:show-snackbar
                                          {:level :error
                                           :text (or (:message error) (str error))}]))}]]]})))

(defn- merged-conn-ids [db guardrail-id new-selected-set]
  (let [originals (get-in db (conj state-path :original-ids guardrail-id) #{})]
    (vec (into originals new-selected-set))))

(rf/reg-event-fx
 :guardrails-suggestions/toggle-existing-role
 (fn [{:keys [db]} [_ guardrail conn-id on?]]
   (let [id (:id guardrail)
         current (get-in db (conj state-path :new-selected id) #{})
         next-set (if on? (conj current conn-id) (disj current conn-id))
         next-db (assoc-in db (conj state-path :new-selected id) next-set)]
     {:db next-db
      :fx [[:dispatch [:guardrails-suggestions/put-existing
                       guardrail
                       (merged-conn-ids next-db id next-set)]]]})))

(rf/reg-event-fx
 :guardrails-suggestions/toggle-existing-checkbox
 (fn [{:keys [db]} [_ guardrail all-new-conn-ids]]
   (let [id (:id guardrail)
         currently-checked? (seq (get-in db (conj state-path :new-selected id) #{}))
         next-set (if currently-checked? #{} (set all-new-conn-ids))
         next-db (assoc-in db (conj state-path :new-selected id) next-set)]
     {:db next-db
      :fx (cond-> [[:dispatch [:guardrails-suggestions/put-existing
                               guardrail
                               (merged-conn-ids next-db id next-set)]]]
            (and (not currently-checked?) (seq next-set))
            (conj [:dispatch [:guardrails-suggestions/open-item id]]))})))

;; Click on the suggestion's main checkbox.
;; - if already exists -> DELETE
;; - if not exists     -> ensure all available roles are toggled on, then POST
(rf/reg-event-fx
 :guardrails-suggestions/toggle-checkbox
 (fn [{:keys [db]} [_ suggestion all-conn-ids]]
   (let [sname (:name suggestion)
         existing-id (get-in db (conj state-path :existing sname))]
     (if existing-id
       {:fx [[:dispatch [:guardrails-suggestions/delete sname]]]}
       (let [conn-set (set all-conn-ids)]
         {:db (assoc-in db (conj state-path :selected-toggles sname) conn-set)
          :fx [[:dispatch [:guardrails-suggestions/open-item sname]]
               [:dispatch [:guardrails-suggestions/create suggestion conn-set]]]})))))
