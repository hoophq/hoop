(ns webapp.settings.views.user-form
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.divider :as divider]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]
            [webapp.components.multiselect :as multi-select]
            [webapp.formatters :as formatters]))

(defmulti dispatch-form identity)
(defmethod dispatch-form :create
  [_ form-fields]
  (rf/dispatch [:users->create-new-user form-fields]))
(defmethod dispatch-form :update
  [_ form-fields]
  (rf/dispatch [:users->update-user form-fields]))

(defmulti header identity)
(defmethod header :create [_]
  [h/h4 "Create a new user" {:class "pt-regular mb-regular"}])
(defmethod header :update [_ name]
  [h/h4 (str "You are editing " name) {:class "pt-regular mb-regular"}])

(defmulti btn-submit-label identity)
(defmethod btn-submit-label :create [_]
  "Create")
(defmethod btn-submit-label :update [_]
  "Update")

(defn array->select-options [array]
  (map #(into {} {"value" % "label" %}) array))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

(defn main
  [form-type user user-groups]
  (let [groups (r/atom (or (array->select-options (:groups user)) ""))
        name (r/atom (or (:name user) ""))
        email (r/atom (or (:email user) ""))
        status (r/atom (or (:status user) ""))
        slack-id (r/atom (or (:slack_id user) ""))
        new-groups-value (r/atom "")
        new-group? (r/atom false)]
    (fn [_ user]
      [:div
       [header form-type (:name user)]
       [:form
        {:class "mb-regular"
         :on-submit (fn [e]
                      (.preventDefault e)
                      (let [new-groups-list (when @new-group?
                                              (formatters/comma-string-to-list @new-groups-value))
                            payload (merge
                                     {:name @name
                                      :groups (concat new-groups-list (js-select-options->list @groups))
                                      :slack_id @slack-id}
                                     (when (= form-type :create) {:email @email})
                                     (when (= form-type :update) {:id (:id user)
                                                                  :status @status}))]
                        (dispatch-form form-type payload)))}
        [forms/input {:label "Name"
                      :on-change #(reset! name (-> % .-target .-value))
                      :placeholder "Your name"
                      :value @name}]
        [multi-select/main {:label "Groups"
                            :options (array->select-options user-groups)
                            :default-value @groups
                            :on-change #(reset! groups (js->clj %))}]
        [:div {:class "grid grid-cols-1 justify-items-end mb-4"}
         (button/secondary {:text "Add new group"
                            :on-click #(reset! new-group? (not @new-group?))
                            :variant :small})]
        (when @new-group?
          [forms/input {:label "Add new groups"
                        :on-change #(reset! new-groups-value (-> % .-target .-value))
                        :placeholder "Comma separated values"
                        :value @new-groups-value}])
        (when (= form-type :create)
          [forms/input {:label "Email"
                        :type "email"
                        :on-change #(reset! email (-> % .-target .-value))
                        :placeholder "user@yourcompany.com"
                        :value @email
                        :required true}])

        (when (= form-type :update)
          [forms/select {:label "Status"
                         :on-change #(reset! status (-> % .-target .-value))
                         :selected @status
                         :options [{:value "active" :text "active"}
                                   {:value "inactive" :text "inactive"}
                                   {:value "reviewing" :text "reviewing"}]
                         :required true}])
        [forms/input {:label "Slack ID"
                      :on-change #(reset! slack-id (-> % .-target .-value))
                      :value @slack-id}]
        [divider/main]
        [:div {:class "grid grid-cols-2 gap-regular"}
         [button/secondary {:text "Cancel"
                            :full-width true
                            :outlined true
                            :on-click #(rf/dispatch [:close-modal])}]
         [button/primary {:text [btn-submit-label form-type]
                          :full-width true
                          :type "submit"}]]]])))
