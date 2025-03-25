(ns webapp.events.runbooks-plugin
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]))

(rf/reg-event-fx
 :runbooks-plugin->get-runbooks
 (fn
   [{:keys [db]} [_ connections-selected]]
   {:fx [[:dispatch
          [:fetch
           {:method "GET"
            :uri "/connections"
            :on-success (fn [connections]
                          (let [connections-names-map (if (empty? connections-selected)
                                                        (mapv #(:name %) connections)
                                                        connections-selected)]
                            (rf/dispatch [:connections->set-connections connections])

                            (rf/dispatch [:fetch {:method "GET"
                                                  :uri "/plugins/runbooks/templates"
                                                  :on-success (fn [runbooks]
                                                                (let [runbooks-filtered-by-connections
                                                                      (filterv
                                                                       (fn [runb]
                                                                         (some #(contains? (set connections-names-map) %) (:connections runb)))
                                                                       (:items runbooks))]

                                                                  (rf/dispatch [:runbooks-plugin->set-runbooks runbooks-filtered-by-connections])

                                                                  (let [search-term (get-in db [:search :term] "")]
                                                                    (if (empty? search-term)
                                                                      (rf/dispatch [:runbooks-plugin->set-filtered-runbooks
                                                                                    (map #(into {} {:name (:name %)})
                                                                                         runbooks-filtered-by-connections)])
                                                                      (rf/dispatch [:search/filter-runbooks search-term])))))
                                                  :on-failure (fn []
                                                                (rf/dispatch [:runbooks-plugin->error-runbooks]))}])))}]]]
    :db (assoc db :runbooks-plugin->runbooks {:status :loading :data nil})}))

(rf/reg-event-fx
 :runbooks-plugin->set-runbooks
 (fn
   [{:keys [db]} [_ runbooks]]
   (let [search-term (get-in db [:search :term] "")]
     {:db (assoc db :runbooks-plugin->runbooks {:status :ready
                                                :data runbooks})
      ;; Aplicar o filtro atual aos novos runbooks carregados
      :fx [(when (seq search-term)
             [:dispatch [:search/filter-runbooks search-term]])
           ;; Caso contrário, definir todos os runbooks como filtrados
           (when (empty? search-term)
             [:dispatch [:runbooks-plugin->set-filtered-runbooks
                         (map #(into {} {:name (:name %)}) runbooks)]])]})))

(rf/reg-event-fx
 :runbooks-plugin->get-runbooks-by-connection
 (fn
   [{:keys [db]} [_ connection-name]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/plugins/runbooks/connections/" connection-name "/templates")
                             :on-success (fn [res]
                                           (rf/dispatch
                                            [:runbooks-plugin->set-runbooks-by-connection
                                             {:runbooks res
                                              :status :ready
                                              :message ""}]))
                             :on-failure (fn [error]
                                           (rf/dispatch
                                            [:runbooks-plugin->set-runbooks-by-connection
                                             {:runbooks nil
                                              :status :error
                                              :message error}]))}]]]
    :db (assoc db :runbooks-plugin->runbook-by-connection {:status :loading :data nil})}))

(rf/reg-event-db
 :runbooks-plugin->set-runbooks-by-connection
 (fn
   [db [_ {:keys [runbooks status message]}]]
   (assoc db :runbooks-plugin->runbooks-by-connection {:status status
                                                       :data runbooks
                                                       :message message})))

(rf/reg-event-db
 :runbooks-plugin->set-filtered-runbooks
 (fn
   [db [_ runbooks]]
   (assoc db :runbooks-plugin->filtered-runbooks runbooks)))

(rf/reg-event-db
 :runbooks-plugin->clear-runbooks
 (fn
   [db [_ template]]
   (assoc db :runbooks-plugin->runbooks {:status :ready :data nil})))

(rf/reg-event-db
 :runbooks-plugin->error-runbooks
 (fn
   [db [_ runbooks]]
   (assoc db :runbooks-plugin->runbooks {:status :error :data nil})))

(rf/reg-event-db
 :runbooks-plugin->set-active-runbook
 (fn
   [db [_ template]]
   (assoc db :runbooks-plugin->selected-runbooks {:status :ready
                                                  :data {:name (:name template)
                                                         :error (:error template)
                                                         :params (keys (:metadata template))
                                                         :file_url (:file_url template)
                                                         :metadata (:metadata template)
                                                         :connections (:connections template)}})))

(rf/reg-event-db
 :runbooks-plugin->clear-active-runbooks
 (fn
   [db [_ template]]
   (assoc db :runbooks-plugin->selected-runbooks {:status :idle :data nil})))

(rf/reg-event-fx
 :runbooks-plugin->run-runbook
 (fn
   [{:keys [db]} [_ {:keys [file-name params connection-name]}]]
   (let [payload {:file_name file-name
                  :parameters params}
         on-failure (fn [error-message error]
                      (rf/dispatch [:show-snackbar {:text error-message :level :error}])
                      (js/setTimeout
                       #(rf/dispatch [:audit->get-session-by-id {:id (:session_id error) :verb "exec"}]) 4000))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "The Runbook was run!"}])

                     ;; This is not the right approach.
                     ;; This was added until we create a better result from runbooks exec
                      (js/setTimeout
                       #(rf/dispatch [:audit->get-session-by-id {:id (:session_id res) :verb "exec"}]) 4000))]
     {:fx [[:dispatch [:fetch {:method "POST"
                               :uri (str "/plugins/runbooks/connections/" connection-name "/exec")
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))

(defn- encode-b64 [data]
  (try
    (js/btoa data)
    (catch js/Error _ (str ""))))

(rf/reg-event-fx
 :runbooks-plugin->git-config
 (fn
   [{:keys [db]} [_ {:keys [git-url git-ssh-key]}]]
   (let [payload (if (cs/blank? git-ssh-key)
                   {:GIT_URL (encode-b64 git-url)}
                   {:GIT_URL (encode-b64 git-url)
                    :GIT_SSH_KEY (encode-b64 git-ssh-key)})
         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:text error :level :error}]))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "Git repository configured!"}]))]
     {:fx [[:dispatch [:fetch {:method "PUT"
                               :uri (str "/plugins/runbooks/config")
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))
