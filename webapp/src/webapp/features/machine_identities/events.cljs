(ns webapp.features.machine-identities.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :machine-identities/list
 (fn [{:keys [db]} [_]]
   {:db (assoc-in db [:machine-identities :status] :loading)
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/machineidentities"
                      :on-success (fn [identities]
                                    (rf/dispatch [:machine-identities/set-identities (or identities [])]))
                      :on-failure (fn [error]
                                    (rf/dispatch [:machine-identities/set-identities []])
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text "Failed to load machine identities"
                                                                  :details error}]))}]]]}))

(rf/reg-event-db
 :machine-identities/set-identities
 (fn [db [_ identities]]
   (-> db
       (assoc-in [:machine-identities :data] identities)
       (assoc-in [:machine-identities :status] :success))))

(rf/reg-event-fx
 :machine-identities/get-identity
 (fn [{:keys [db]} [_ identity-name]]
   {:db (assoc-in db [:machine-identities :current-identity] nil)
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/machineidentities/" identity-name)
                      :on-success (fn [identity]
                                    (rf/dispatch [:machine-identities/set-current-identity identity]))
                      :on-failure (fn [error]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text "Failed to load machine identity"
                                                                  :details error}]))}]]]}))

(rf/reg-event-db
 :machine-identities/set-current-identity
 (fn [db [_ identity]]
   (assoc-in db [:machine-identities :current-identity] identity)))

(rf/reg-event-fx
 :machine-identities/create
 (fn [_ [_ identity-data]]
   {:fx [[:dispatch [:fetch
                     {:method "POST"
                      :uri "/machineidentities"
                      :body identity-data
                      :on-success (fn [response]
                                    (let [identity-name (:name response)]
                                      (rf/dispatch [:show-snackbar {:level :success
                                                                    :text "Machine identity created successfully"}])
                                      (rf/dispatch [:machine-identities/list])
                                      (rf/dispatch [:navigate :machine-identities-roles {} :identity-name identity-name])))
                      :on-failure (fn [error]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text (or (:message error) "Failed to create machine identity")
                                                                  :details error}]))}]]]}))

(rf/reg-event-fx
 :machine-identities/update
 (fn [_ [_ identity-name identity-data]]
   {:fx [[:dispatch [:fetch
                     {:method "PUT"
                      :uri (str "/machineidentities/" identity-name)
                      :body identity-data
                      :on-success (fn [response]
                                    (let [new-creds (seq (:new_credentials response))]
                                      (rf/dispatch [:show-snackbar
                                                    {:level :success
                                                     :text (if new-creds
                                                             (str "Machine identity updated. " (count new-creds) " new credential(s) provisioned.")
                                                             "Machine identity updated successfully")}])
                                      (rf/dispatch [:machine-identities/list])
                                      (rf/dispatch [:navigate :machine-identities])))
                      :on-failure (fn [error]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text (or (:message error) "Failed to update machine identity")
                                                                  :details error}]))}]]]}))

(rf/reg-event-fx
 :machine-identities/delete
 (fn [_ [_ identity-name]]
   {:fx [[:dispatch [:fetch
                     {:method "DELETE"
                      :uri (str "/machineidentities/" identity-name)
                      :on-success (fn []
                                    (rf/dispatch [:show-snackbar {:level :success
                                                                  :text "Machine identity deleted successfully"}])
                                    (rf/dispatch [:machine-identities/list]))
                      :on-failure (fn [error]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text (or (:message error) "Failed to delete machine identity")
                                                                  :details error}]))}]]]}))

(rf/reg-event-fx
 :machine-identities/list-credentials
 (fn [{:keys [db]} [_ identity-name]]
   {:db (assoc-in db [:machine-identities :credentials :status] :loading)
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/machineidentities/" identity-name "/credentials")
                      :on-success (fn [credentials]
                                    (rf/dispatch [:machine-identities/set-credentials (or credentials [])]))
                      :on-failure (fn [error]
                                    (rf/dispatch [:machine-identities/set-credentials []])
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text "Failed to load credentials"
                                                                  :details error}]))}]]]}))

(rf/reg-event-db
 :machine-identities/set-credentials
 (fn [db [_ credentials]]
   (-> db
       (assoc-in [:machine-identities :credentials :data] credentials)
       (assoc-in [:machine-identities :credentials :status] :success))))

(rf/reg-event-fx
 :machine-identities/rotate-credential
 (fn [_ [_ {:keys [identity-name connection-name on-complete]}]]
   {:fx [[:dispatch [:fetch
                     {:method "POST"
                      :uri (str "/machineidentities/" identity-name "/credentials/" connection-name "/rotate")
                      :on-success (fn [new-credential]
                                    (rf/dispatch [:machine-identities/replace-credential new-credential])
                                    (rf/dispatch [:show-snackbar {:level :success
                                                                  :text (str "Credential rotated for " connection-name)}])
                                    (when on-complete (on-complete)))
                      :on-failure (fn [error]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text (or (:message error) "Failed to rotate credential")
                                                                  :details error}])
                                    (when on-complete (on-complete)))}]]]}))

(rf/reg-event-db
 :machine-identities/replace-credential
 (fn [db [_ new-credential]]
   (let [conn-name (:connection_name new-credential)
         credentials (get-in db [:machine-identities :credentials :data] [])
         updated (mapv #(if (= (:connection_name %) conn-name) new-credential %) credentials)]
     (assoc-in db [:machine-identities :credentials :data] updated))))

(rf/reg-event-db
 :machine-identities/clear-current-identity
 (fn [db [_]]
   (assoc-in db [:machine-identities :current-identity] nil)))
