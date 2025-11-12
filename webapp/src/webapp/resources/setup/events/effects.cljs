(ns webapp.resources.setup.events.effects
  (:require
   [clojure.string :as str]
   [re-frame.core :as rf]
   [webapp.resources.setup.events.process-form :as process-form]
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
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/agents"
                      :on-success (fn [agents]
                                    (let [created-agent (->> agents
                                                             (filter #(= (:name %) agent-name))
                                                             first)
                                          agent-id (:id created-agent)]
                                      (rf/dispatch [:resource-setup->set-agent-id agent-id])
                                      (rf/dispatch [:webapp.events.agents/set-agents agents])))
                      :on-failure (fn [error]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text "Failed to fetch agents"
                                                                  :details error}]))}]]]}))


(rf/reg-event-fx
 :resource-setup->clear-agent-state
 (fn [{:keys [db]} _]
   {:db (-> db
            (assoc-in [:resource-setup :agent-id] nil)
            (assoc-in [:resource-setup :agent-creation-mode] nil))}))

;; Role management
(rf/reg-event-db
 :resource-setup->add-role
 (fn [db [_]]
   (let [resource-type (get-in db [:resource-setup :type])
         resource-subtype (get-in db [:resource-setup :subtype])
         command (get-in db [:resource-setup :command])
         new-role {:name (helpers/random-role-name resource-subtype)
                   :type resource-type
                   :subtype resource-subtype
                   :command command
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

;; Environment variables for roles - New pattern with current-key/current-value
(rf/reg-event-db
 :resource-setup->update-role-env-current-key
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :env-current-key] value)))

(rf/reg-event-db
 :resource-setup->update-role-env-current-value
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :env-current-value] value)))

(rf/reg-event-db
 :resource-setup->add-role-env-row
 (fn [db [_ role-index]]
   (let [current-key (get-in db [:resource-setup :roles role-index :env-current-key])
         current-value (get-in db [:resource-setup :roles role-index :env-current-value])]
     (if (and (not (str/blank? current-key)) (not (str/blank? current-value)))
       (-> db
           (update-in [:resource-setup :roles role-index :environment-variables]
                      (fnil conj []) {:key current-key :value current-value})
           (assoc-in [:resource-setup :roles role-index :env-current-key] "")
           (assoc-in [:resource-setup :roles role-index :env-current-value] ""))
       db))))

(rf/reg-event-db
 :resource-setup->update-role-env-var
 (fn [db [_ role-index var-index field value]]
   (assoc-in db [:resource-setup :roles role-index :environment-variables var-index field] value)))

;; Configuration files for roles - New pattern with current-name/current-content
(rf/reg-event-db
 :resource-setup->update-role-config-current-name
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :config-current-name] value)))

(rf/reg-event-db
 :resource-setup->update-role-config-current-content
 (fn [db [_ role-index value]]
   (assoc-in db [:resource-setup :roles role-index :config-current-content] value)))

(rf/reg-event-db
 :resource-setup->add-role-config-row
 (fn [db [_ role-index]]
   (let [current-name (get-in db [:resource-setup :roles role-index :config-current-name])
         current-content (get-in db [:resource-setup :roles role-index :config-current-content])]
     (if (and (not (str/blank? current-name)) (not (str/blank? current-content)))
       (-> db
           (update-in [:resource-setup :roles role-index :configuration-files]
                      (fnil conj []) {:key current-name :value current-content})
           (assoc-in [:resource-setup :roles role-index :config-current-name] "")
           (assoc-in [:resource-setup :roles role-index :config-current-content] ""))
       db))))

(rf/reg-event-db
 :resource-setup->update-role-config-file
 (fn [db [_ role-index file-index field value]]
   (assoc-in db [:resource-setup :roles role-index :configuration-files file-index field] value)))

;; Submit
(rf/reg-event-fx
 :resource-setup->submit
 (fn [{:keys [db]} _]
   (let [payload (process-form/process-payload db)
         ;; Armazena as roles processadas para usar no success step
         processed-roles (:roles payload)]
     {:db (assoc-in db [:resource-setup :processed-roles] processed-roles)
      :fx [[:dispatch [:resources->create-resource payload]]]})))

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

