(ns webapp.features.users.views.user-form
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [clojure.string :as str]
            ["@radix-ui/themes" :refer [Flex Heading Box Button Text]]
            ["@radix-ui/react-icons" :refer [EyeOpenIcon EyeClosedIcon CopyIcon]]
            ["unique-names-generator" :as ung]
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
        see-password? (r/atom false)
        password (r/atom (ung/uniqueNamesGenerator #js{:dictionaries #js[ung/animals ung/colors ung/adjectives]
                                                       :style :capital
                                                       :separator "-"
                                                       :length 3}))
        gateway-public-info (rf/subscribe [:gateway->public-info])]
    (fn [_ user]
      [:div
       [header form-type (:name user)]
       [:form
        {:class "space-y-radix-5"
         :on-submit (fn [e]
                      (.preventDefault e)
                      (let [payload (merge
                                     {:name @name
                                      :groups (js-select-options->list @groups)
                                      :slack_id @slack-id
                                      :email @email}
                                     (when (= form-type :update) {:id (:id user)
                                                                  :status @status})
                                     (when (and (= (-> @gateway-public-info :data :auth_method)
                                                   "local")
                                                (= form-type :create))
                                       {:password @password}))]
                        (dispatch-form form-type payload)))}
        [forms/input {:label "Name"
                      :on-change #(reset! name (-> % .-target .-value))
                      :placeholder "Your name"
                      :value @name}]
        [multi-select/creatable-select
         {:label "Groups"
          :options (array->select-options user-groups)
          :default-value @groups
          :on-change #(reset! groups (js->clj %))}]
        (when (= form-type :create)
          [forms/input {:label "Email"
                        :type "email"
                        :not-margin-bottom? true
                        :on-change #(reset! email (-> % .-target .-value))
                        :placeholder "user@yourcompany.com"
                        :value @email
                        :required true}])

        (when (= form-type :update)
          [forms/select {:label "Status"
                         :name "user-status"
                         :not-margin-bottom? true
                         :on-change #(reset! status %)
                         :selected @status
                         :full-width? true
                         :options [{:value "active" :text "active"}
                                   {:value "inactive" :text "inactive"}
                                   {:value "reviewing" :text "reviewing"}]
                         :required true}])
        [forms/input {:label "Slack ID"
                      :not-margin-bottom? true
                      :on-change #(reset! slack-id (-> % .-target .-value))
                      :value @slack-id}]

        (when (and (= (-> @gateway-public-info :data :auth_method) "local")
                   (= form-type :create))
          [:<>
           [:> Heading {:size "4" :mb "1" :pt "2"} "Password"]
           [:> Box {:mb "2"}
            [:> Text {:size "1"}
             "Copy and send this password to the invited user. You can see this password only this time"]]
           [:> Flex {:align "center" :gap "2" :mb "4"}
            [:> Box {:flexGrow "1" :mb "2"}
             [:> Text {:size 2 :color "gray"}
              (if @see-password?
                @password
                (str/join "" (repeat (count @password) "*")))]]
            (if @see-password?
              [:> EyeClosedIcon {:color "gray"
                                 :cursor "pointer"
                                 :on-click #(reset! see-password? (not @see-password?))}]
              [:> EyeOpenIcon {:color "gray"
                               :cursor "pointer"
                               :on-click #(reset! see-password? (not @see-password?))}])
            [:> CopyIcon {:color "gray"
                          :cursor "pointer"
                          :onClick (fn []
                                     (js/navigator.clipboard.writeText @password)
                                     (rf/dispatch [:show-snackbar {:level :success
                                                                   :text "Password copied to clipboard"}]))}]]])
        [:div {:class "grid grid-cols-2 gap-regular"}
         [:> Button {:variant "outline"
                     :type "button"
                     :size "3"
                     :full-width true
                     :on-click #(rf/dispatch [:modal->close])}
          "Cancel"]
         [:> Button {:full-width true
                     :size "3"
                     :type "submit"}
          [btn-submit-label form-type]]]]])))

