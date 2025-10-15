(ns webapp.resources.views.setup.events.effects
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.resources.views.setup.events.process-form :as process-form]
   [webapp.resources.helpers :as helpers]))

(rf/reg-event-db
 :resource-setup->initialize-state
 (fn [db [_ initial-data]]
   (if initial-data
     (assoc db :resource-setup initial-data)
     (assoc db :resource-setup {:current-step :resource-name
                                :roles []}))))

(rf/reg-event-fx
 :resource-setup->initialize-from-catalog
 (fn [{:keys [db]} [_ {:keys [type subtype command]}]]
   (js/console.log "🔧 Initializing resource setup from catalog:" (clj->js {:type type :subtype subtype}))
   {:db (update db :resource-setup merge {:type type
                                          :subtype subtype
                                          :command command
                                          :current-step :resource-name
                                          :from-catalog? true
                                          :name ""
                                          :agent-id nil
                                          :roles []})
    :fx []}))

(rf/reg-event-db
 :resource-setup->set-step
 (fn [db [_ step]]
   (assoc-in db [:resource-setup :current-step] step)))

(rf/reg-event-db
 :resource-setup->set-resource-name
 (fn [db [_ name]]
   (assoc-in db [:resource-setup :name] name)))

(rf/reg-event-db
 :resource-setup->set-agent-id
 (fn [db [_ agent-id]]
   (assoc-in db [:resource-setup :agent-id] agent-id)))

(rf/reg-event-db
 :resource-setup->set-agent-creation-mode
 (fn [db [_ mode]]
   (assoc-in db [:resource-setup :agent-creation-mode] mode)))

;; Fetch agent ID by name after creation
(rf/reg-event-fx
 :resource-setup->fetch-agent-id-by-name
 (fn [_ [_ agent-name]]
   (js/console.log "🔎 Fetching agents to find ID for:" agent-name)
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success (fn [agents]
                                    (js/console.log "📋 Agents received:" (clj->js agents))
                                    ;; Find agent by name
                                    (let [created-agent (->> agents
                                                             (filter #(= (:name %) agent-name))
                                                             first)
                                          agent-id (:id created-agent)]
                                      (js/console.log "🎯 Found agent:" (clj->js created-agent))
                                      (js/console.log "🆔 Agent ID:" agent-id)
                                      (if agent-id
                                        (do
                                          (js/console.log "✅ Setting agent-id in resource setup:" agent-id)
                                          (rf/dispatch [:resource-setup->set-agent-id agent-id])
                                          ;; Also update agents list in state
                                          (rf/dispatch [:webapp.events.agents/set-agents agents]))
                                        (js/console.warn "⚠️ Agent not found in list"))))
                      :on-failure (fn [error]
                                    (js/console.error "❌ Failed to fetch agents:" error))}]]]}))


;; Role management
(rf/reg-event-db
 :resource-setup->add-role
 (fn [db [_]]
   (let [resource-type (get-in db [:resource-setup :type])
         resource-subtype (get-in db [:resource-setup :subtype])
         new-role {:name (helpers/random-role-name resource-subtype)
                   :type resource-type
                   :subtype resource-subtype
                   :credentials {}
                   :environment-variables []
                   :configuration-files []}]
     (update-in db [:resource-setup :roles] (fnil conj []) new-role))))

(rf/reg-event-db
 :resource-setup->remove-role
 (fn [db [_ role-index]]
   (update-in db [:resource-setup :roles]
              (fn [roles]
                (vec (concat (subvec roles 0 role-index)
                             (subvec roles (inc role-index))))))))

(rf/reg-event-db
 :resource-setup->update-role-name
 (fn [db [_ role-index name]]
   (assoc-in db [:resource-setup :roles role-index :name] name)))

(rf/reg-event-db
 :resource-setup->update-role-credentials
 (fn [db [_ role-index key value]]
   (assoc-in db [:resource-setup :roles role-index :credentials key] value)))

(rf/reg-event-db
 :resource-setup->update-role-metadata-credentials
 (fn [db [_ role-index key value]]
   (assoc-in db [:resource-setup :roles role-index :metadata-credentials key] value)))

;; Environment variables for roles
(rf/reg-event-db
 :resource-setup->add-role-env-var
 (fn [db [_ role-index key value]]
   (if (and (not (str/blank? key)) (not (str/blank? value)))
     (update-in db [:resource-setup :roles role-index :environment-variables]
                (fnil conj []) {:key key :value value})
     db)))

(rf/reg-event-db
 :resource-setup->remove-role-env-var
 (fn [db [_ role-index var-index]]
   (update-in db [:resource-setup :roles role-index :environment-variables]
              (fn [vars]
                (vec (concat (subvec vars 0 var-index)
                             (subvec vars (inc var-index))))))))

;; Configuration files for roles
(rf/reg-event-db
 :resource-setup->add-role-config-file
 (fn [db [_ role-index name content]]
   (if (and (not (str/blank? name)) (not (str/blank? content)))
     (update-in db [:resource-setup :roles role-index :configuration-files]
                (fnil conj []) {:key name :value content})
     db)))

(rf/reg-event-db
 :resource-setup->remove-role-config-file
 (fn [db [_ role-index file-index]]
   (update-in db [:resource-setup :roles role-index :configuration-files]
              (fn [files]
                (vec (concat (subvec files 0 file-index)
                             (subvec files (inc file-index))))))))

;; Submit
(rf/reg-event-fx
 :resource-setup->submit
 (fn [{:keys [db]} _]
   (let [payload (process-form/process-payload db)]
     {:fx [[:dispatch [:resources->create-resource payload]]]})))

;; Navigation helpers
(rf/reg-event-fx
 :resource-setup->next-step
 (fn [{:keys [db]} [_ next-step]]
   {:db (assoc-in db [:resource-setup :current-step] next-step)}))

(rf/reg-event-fx
 :resource-setup->back
 (fn [{:keys [db]} _]
   (let [current-step (get-in db [:resource-setup :current-step])]
     {:db (assoc-in db [:resource-setup :current-step]
                    (case current-step
                      :agent-selector :resource-name
                      :roles :agent-selector
                      :success :roles
                      :resource-name))})))

