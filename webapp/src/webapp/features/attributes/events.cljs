(ns webapp.features.attributes.events
  (:require
   [clojure.set :as set]
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :attributes/list
 (fn [{:keys [db]} _]
   {:db (assoc-in db [:attributes :list :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri "/attributes"
                             :on-success #(rf/dispatch [:attributes/list-success %])
                             :on-failure #(rf/dispatch [:attributes/list-failure %])}]]]}))

(rf/reg-event-db
 :attributes/list-success
 (fn [db [_ response]]
   (update-in db [:attributes :list] merge {:status :success :data (or (:data response) [])})))

(rf/reg-event-fx
 :attributes/list-failure
 (fn [{:keys [db]} [_ error]]
   {:db (update-in db [:attributes :list] merge {:status :error :error error})
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load attributes"
                                     :details error}]]]}))

(rf/reg-event-fx
 :attributes/get
 (fn [{:keys [db]} [_ attr-name]]
   {:db (assoc-in db [:attributes :active :status] :loading)
    :fx [[:dispatch [:fetch {:method "GET"
                             :uri (str "/attributes/" attr-name)
                             :on-success #(rf/dispatch [:attributes/get-success %])
                             :on-failure #(rf/dispatch [:attributes/get-failure %])}]]]}))

(rf/reg-event-db
 :attributes/get-success
 (fn [db [_ data]]
   (update-in db [:attributes :active] merge {:status :success :data data})))

(rf/reg-event-fx
 :attributes/get-failure
 (fn [{:keys [db]} [_ error]]
   {:db (update-in db [:attributes :active] merge {:status :error :error error})
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to load attribute"
                                     :details error}]]]}))

(rf/reg-event-fx
 :attributes/create
 (fn [{:keys [db]} [_ attr-data]]
   {:db (assoc-in db [:attributes :submitting?] true)
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/attributes"
                             :body attr-data
                             :on-success #(rf/dispatch [:attributes/create-success attr-data])
                             :on-failure #(rf/dispatch [:attributes/create-failure %])}]]]}))

(rf/reg-event-fx
 :attributes/create-inline
 (fn [{:keys [db]} [_ attr-data]]
   {:db (assoc-in db [:attributes :submitting?] true)
    :fx [[:dispatch [:fetch {:method "POST"
                             :uri "/attributes"
                             :body attr-data
                             :on-success #(rf/dispatch [:attributes/create-inline-success attr-data])
                             :on-failure #(rf/dispatch [:attributes/create-failure %])}]]]}))

(rf/reg-event-fx
 :attributes/create-success
 (fn [{:keys [db]} [_ attr-data]]
   {:db (assoc-in db [:attributes :submitting?] false)
    :fx [[:dispatch [:navigate :settings-attributes]]
         [:dispatch [:show-snackbar {:level :success
                                     :text (str "Attribute '" (:name attr-data) "' created successfully!")}]]]}))

(rf/reg-event-fx
 :attributes/create-inline-success
 (fn [{:keys [db]} [_ attr-data]]
   {:db (assoc-in db [:attributes :submitting?] false)
    :fx [[:dispatch [:attributes/list]]
         [:dispatch [:show-snackbar {:level :success
                                     :text (str "Attribute '" (:name attr-data) "' created successfully!")}]]]}))

(rf/reg-event-fx
 :attributes/create-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:attributes :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to create attribute"
                                     :details error}]]]}))

(rf/reg-event-fx
 :attributes/update
 (fn [{:keys [db]} [_ attr-name attr-data]]
   {:db (assoc-in db [:attributes :submitting?] true)
    :fx [[:dispatch [:fetch {:method "PUT"
                             :uri (str "/attributes/" attr-name)
                             :body attr-data
                             :on-success #(rf/dispatch [:attributes/update-success attr-name])
                             :on-failure #(rf/dispatch [:attributes/update-failure %])}]]]}))

(rf/reg-event-fx
 :attributes/update-success
 (fn [{:keys [db]} [_ attr-name]]
   {:db (assoc-in db [:attributes :submitting?] false)
    :fx [[:dispatch [:navigate :settings-attributes]]
         [:dispatch [:show-snackbar {:level :success
                                     :text (str "Attribute '" attr-name "' updated successfully!")}]]]}))

(rf/reg-event-fx
 :attributes/update-failure
 (fn [{:keys [db]} [_ error]]
   {:db (assoc-in db [:attributes :submitting?] false)
    :fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to update attribute"
                                     :details error}]]]}))

(rf/reg-event-fx
 :attributes/delete
 (fn [_ [_ attr-name]]
   {:fx [[:dispatch [:fetch {:method "DELETE"
                             :uri (str "/attributes/" attr-name)
                             :on-success #(rf/dispatch [:attributes/delete-success attr-name])
                             :on-failure #(rf/dispatch [:attributes/delete-failure %])}]]]}))

(rf/reg-event-fx
 :attributes/delete-success
 (fn [_ [_ attr-name]]
   {:fx [[:dispatch [:navigate :settings-attributes]]
         [:dispatch [:show-snackbar {:level :success
                                     :text (str "Attribute '" attr-name "' deleted successfully!")}]]]}))

(rf/reg-event-fx
 :attributes/delete-failure
 (fn [_ [_ error]]
   {:fx [[:dispatch [:show-snackbar {:level :error
                                     :text "Failed to delete attribute"
                                     :details error}]]]}))

(rf/reg-event-db
 :attributes/clear-active
 (fn [db _]
   (assoc-in db [:attributes :active] {:status :idle :data nil})))

(rf/reg-event-fx
 :attributes/update-for-connection
 (fn [{:keys [db]} [_ connection-name selected-names initial-names]]
   (let [all-attributes (get-in db [:attributes :list :data] [])
         attr-by-name   (into {} (map (juxt :name identity)) all-attributes)
         selected-set   (set selected-names)
         initial-set    (set initial-names)
         added          (set/difference selected-set initial-set)
         removed        (set/difference initial-set selected-set)
         make-update-fx (fn [attr-name add?]
                          (let [attr (get attr-by-name attr-name)
                                current-conns (set (or (:connection_names attr) []))
                                new-conns (if add?
                                            (conj current-conns connection-name)
                                            (disj current-conns connection-name))
                                body (cond-> {:name attr-name
                                              :connection_names (vec new-conns)}
                                       (:description attr) (assoc :description (:description attr))
                                       (seq (:access_request_rule_names attr)) (assoc :access_request_rule_names (:access_request_rule_names attr))
                                       (seq (:access_control_group_names attr)) (assoc :access_control_group_names (:access_control_group_names attr)))]
                            [:dispatch [:fetch {:method "PUT"
                                                :uri (str "/attributes/" attr-name)
                                                :body body
                                                :on-success #(rf/dispatch [:attributes/list])
                                                :on-failure #(rf/dispatch [:show-snackbar
                                                                           {:level :error
                                                                            :text (str "Failed to update attribute '" attr-name "'")
                                                                            :details %}])}]]))]
     {:fx (concat
           (map #(make-update-fx % true) added)
           (map #(make-update-fx % false) removed))})))

