(ns webapp.features.activation-journey.events
  (:require
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.features.activation-journey.templates :as templates]))

;; State path: [:activation-journey]
;;
;; Shape:
;; {:masking-rules {:status :idle|:loading|:success|:error :data [...]}
;;  :roles         [{:id <connection-id> :name <role-name>} ...]
;;  :terminal-banner-index      <int>
;;  :terminal-banner-dismissed? <bool>}

(defn- admin? [db]
  (boolean (get-in db [:users->current-user :data :admin?])))

(defn- parse-csv [value]
  (if (seq value)
    (vec (remove cs/blank? (cs/split value #",")))
    []))

(defn- role-names-from-db
  "Role names created by the current resource flow. Mirrors the sources the
  resource setup uses: processed roles (setup flow) and last-created roles
  (add-role flow)."
  [db]
  (let [processed (get-in db [:resource-setup :processed-roles] [])
        last-created (get-in db [:resources :last-created-roles] [])]
    (->> (concat processed last-created)
         (keep :name)
         distinct
         vec)))

;; Idempotent bootstrap dispatched by every activation-journey surface on
;; mount. All backing endpoints are admin-only, so this is a no-op for
;; non-admin users (their surfaces render nothing).
(rf/reg-event-fx
 :activation-journey/init
 (fn [{:keys [db]} [_ {:keys [with-roles?]}]]
   (if-not (admin? db)
     {}
     (let [role-names (when with-roles? (role-names-from-db db))]
       {:db (cond-> db
              with-roles? (assoc-in [:activation-journey :roles] []))
        :fx (cond-> [[:dispatch [:guardrails->get-all]]
                     [:dispatch [:ai-session-analyzer/get-rules]]
                     [:dispatch [:ai-session-analyzer/get-provider]]
                     [:dispatch [:activation-journey/fetch-masking-rules]]
                     [:dispatch [:gateway->get-info]]]
              (seq role-names)
              (conj [:dispatch [:activation-journey/fetch-roles role-names]]))}))))

(rf/reg-event-fx
 :activation-journey/fetch-masking-rules
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:activation-journey :masking-rules :status] :loading)
    :fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/datamasking-rules"
                   :on-success #(rf/dispatch [:activation-journey/set-masking-rules %])
                   :on-failure #(rf/dispatch [:activation-journey/set-masking-rules-failure %])}]]]}))

(rf/reg-event-db
 :activation-journey/set-masking-rules
 (fn [db [_ rules]]
   (assoc-in db [:activation-journey :masking-rules]
             {:status :success :data (vec (or rules []))})))

;; Promotional surfaces degrade silently on fetch failure — cards and banners
;; simply treat the feature as not configured instead of toasting an error.
(rf/reg-event-db
 :activation-journey/set-masking-rules-failure
 (fn [db [_ error]]
   (assoc-in db [:activation-journey :masking-rules]
             {:status :error :data [] :error error})))

(rf/reg-event-fx
 :activation-journey/fetch-roles
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
                                     (rf/dispatch [:activation-journey/add-role
                                                   {:id (:id conn) :name (:name conn)}]))))
                   :on-failure (fn [_]
                                 (rf/dispatch [:show-snackbar
                                               {:level :error
                                                :text (str "Failed to fetch role: " name)}]))}]])
              role-names)}))

(rf/reg-event-db
 :activation-journey/add-role
 (fn [db [_ role]]
   (let [existing (get-in db [:activation-journey :roles] [])
         already? (some #(= (:id %) (:id role)) existing)]
     (if already?
       db
       (update-in db [:activation-journey :roles] (fnil conj []) role)))))

;; Seeds the guardrail create form (?template=<name>&connections=<csv of ids>).
;; An unknown or stale template id falls back to the regular blank form —
;; a broken deep link must never break the page.
(rf/reg-event-fx
 :activation-journey/seed-guardrail-template
 (fn [_ [_ template-id connections-csv]]
   (if-let [template (templates/find-guardrail-template template-id)]
     {:fx [[:dispatch [:guardrails->set-active-guardrail
                       (-> (templates/guardrail-payload template)
                           (assoc :id ""
                                  :connection_ids (parse-csv connections-csv)))]]]}
     {:fx [[:dispatch [:guardrails->clear-active-guardrail]]]})))
