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

(rf/reg-event-db
 :runbooks-plugin->set-filtered-runbooks
 (fn
   [db [_ runbooks]]
   (assoc db :runbooks-plugin->filtered-runbooks runbooks)))

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
                      (rf/dispatch [:show-snackbar {:text "Failed to execute runbook" :level :error :details error}])
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
   [{:keys [db]} [_ config-data]]
   (let [repository-type (:repository-type config-data)
         credential-type (:credential-type config-data)
         git-url (:git-url config-data)

         ;; Build payload based on repository type and credentials
         base-payload {:GIT_URL (encode-b64 git-url)}

         payload (cond
                   ;; For public repositories, only GIT_URL is needed
                   (= repository-type "public")
                   base-payload

                   ;; For private repositories with HTTP credentials
                   (and (= repository-type "private")
                        (= credential-type "http"))
                   (cond-> base-payload
                     ;; Add HTTP user if provided, otherwise defaults to "oauth2" on server
                     (not (cs/blank? (:http-user config-data)))
                     (assoc :GIT_USER (encode-b64 (:http-user config-data)))

                     ;; HTTP token/password is required for private HTTP repos
                     (not (cs/blank? (:http-token config-data)))
                     (assoc :GIT_PASSWORD (encode-b64 (:http-token config-data))))

                   ;; For private repositories with SSH credentials
                   (and (= repository-type "private")
                        (= credential-type "ssh"))
                   (cond-> base-payload
                     ;; SSH key is required for private SSH repos
                     (not (cs/blank? (:ssh-key config-data)))
                     (assoc :GIT_SSH_KEY (encode-b64 (:ssh-key config-data)))

                     ;; SSH user is optional, defaults to "git" on server
                     (not (cs/blank? (:ssh-user config-data)))
                     (assoc :GIT_SSH_USER (encode-b64 (:ssh-user config-data)))

                     ;; SSH key password is optional
                     (not (cs/blank? (:ssh-key-password config-data)))
                     (assoc :GIT_SSH_KEYPASS (encode-b64 (:ssh-key-password config-data)))

                     ;; SSH known hosts is optional
                     (not (cs/blank? (:ssh-known-hosts config-data)))
                     (assoc :GIT_SSH_KNOWN_HOSTS (encode-b64 (:ssh-known-hosts config-data))))

                   ;; Default fallback
                   :else
                   base-payload)

         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:text "Failed to configure Git repository" :level :error :details error}]))
         on-success (fn [res]
                      (rf/dispatch
                       [:show-snackbar {:level :success
                                        :text "Git repository configured!"}]))]
     {:fx [[:dispatch [:fetch {:method "PUT"
                               :uri "/plugins/runbooks/config"
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))

(rf/reg-event-fx
 :runbooks-plugin->git-config-with-reload
 (fn
   [{:keys [db]} [_ config-data custom-on-success]]
   (let [repository-type (:repository-type config-data)
         credential-type (:credential-type config-data)
         git-url (:git-url config-data)

         ;; Build payload based on repository type and credentials
         base-payload {:GIT_URL (encode-b64 git-url)}

         payload (cond
                   ;; For public repositories, only GIT_URL is needed
                   (= repository-type "public")
                   base-payload

                   ;; For private repositories with HTTP credentials
                   (and (= repository-type "private")
                        (= credential-type "http"))
                   (cond-> base-payload
                     ;; Add HTTP user if provided, otherwise defaults to "oauth2" on server
                     (not (cs/blank? (:http-user config-data)))
                     (assoc :GIT_USER (encode-b64 (:http-user config-data)))

                     ;; HTTP token/password is required for private HTTP repos
                     (not (cs/blank? (:http-token config-data)))
                     (assoc :GIT_PASSWORD (encode-b64 (:http-token config-data))))

                   ;; For private repositories with SSH credentials
                   (and (= repository-type "private")
                        (= credential-type "ssh"))
                   (cond-> base-payload
                     ;; SSH key is required for private SSH repos
                     (not (cs/blank? (:ssh-key config-data)))
                     (assoc :GIT_SSH_KEY (encode-b64 (:ssh-key config-data)))

                     ;; SSH user is optional, defaults to "git" on server
                     (not (cs/blank? (:ssh-user config-data)))
                     (assoc :GIT_SSH_USER (encode-b64 (:ssh-user config-data)))

                     ;; SSH key password is optional
                     (not (cs/blank? (:ssh-key-password config-data)))
                     (assoc :GIT_SSH_KEYPASS (encode-b64 (:ssh-key-password config-data)))

                     ;; SSH known hosts is optional
                     (not (cs/blank? (:ssh-known-hosts config-data)))
                     (assoc :GIT_SSH_KNOWN_HOSTS (encode-b64 (:ssh-known-hosts config-data))))

                   ;; Default fallback
                   :else
                   base-payload)

         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:text "Failed to configure Git repository" :level :error :details error}]))
         on-success (fn [res]
                      ;; Use custom success handler if provided
                      (if custom-on-success
                        (custom-on-success)
                        (rf/dispatch
                         [:show-snackbar {:level :success
                                          :text "Git repository configured!"}])))]
     {:fx [[:dispatch [:fetch {:method "PUT"
                               :uri (str "/plugins/runbooks/config")
                               :on-success on-success
                               :on-failure on-failure
                               :body payload}]]]})))
