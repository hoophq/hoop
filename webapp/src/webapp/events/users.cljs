(ns webapp.events.users
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]))

(rf/reg-event-fx
 :users->get-users
 (fn
   [{:keys [db]} [_ _]]
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/users"
                      :on-success (fn [users]
                                    (rf/dispatch [::users->set-users users]))
                      :on-failure #(println "Not allowed to get Users")}]]]}))

(rf/reg-event-fx
 :users->get-user
 (fn
   [{:keys [db]} [_ _]]
   {:db (assoc db :users->current-user {:loading true
                                        :data (-> db :users->current-user :data)})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/userinfo"
                      :on-success (fn [user]
                                    (rf/dispatch
                                     [:fetch
                                      {:method "GET"
                                       :uri "/serverinfo"
                                       :on-success #(rf/dispatch [::users->set-current-user user %])}]))}]]]}))

(rf/reg-event-fx
 ::users->set-users
 (fn
   [{:keys [db]} [_ users]]
   {:db (assoc db :users users)}))

(rf/reg-event-fx
 ::users->set-current-user
 (fn
   [{:keys [db]} [_ user server-info]]
   (let [license-info (:license_info server-info)]

     {:db (assoc db :users->current-user {:loading false
                                          :data (assoc user
                                                       :user-management? (= (:webapp_users_management user) "on")
                                                       :free-license? (not (and (:is_valid license-info)
                                                                                (= (:type license-info) "enterprise")))
                                                       :admin? (:is_admin user))})
      :fx [[:dispatch [:initialize-intercom user]]
           [:dispatch [:close-page-loader]]]})))

(rf/reg-event-fx
 :users->create-new-user
 (fn
   [{:keys [db]} [_ new-user]]
   (let [success (fn []
                   (rf/dispatch [:close-modal])
                   (rf/dispatch [:users->get-users])
                   (rf/dispatch [:users->get-user-groups])
                   (rf/dispatch [:show-snackbar {:level :success
                                                 :text "User created!"}]))]
     {:fx [[:dispatch [:fetch
                       {:method "POST"
                        :uri "/users"
                        :body new-user
                        :on-success success}]]]})))

(rf/reg-event-fx
 :users->update-user
 (fn
   [{:keys [db]} [_ user]]
   (let [success (fn []
                   (rf/dispatch [:close-modal])
                   (rf/dispatch [:users->get-users])
                   (rf/dispatch [:users->get-user-groups])
                   (rf/dispatch [:show-snackbar {:level :success
                                                 :text "User updated!"}]))]
     {:fx [[:dispatch [:fetch
                       {:method "PUT"
                        :uri (str "/users/" (:id user))
                        :body (dissoc user :id)
                        :on-success success}]]]})))


(rf/reg-event-fx
 :users->update-user-slack-id
 (fn
   [{:keys [db]} [_ user]]
   (let [success (fn []
                   (rf/dispatch [:users->get-users])
                   (rf/dispatch [:users->get-user-groups])
                   (rf/dispatch [:show-snackbar {:level :success
                                                 :text "User slack id updated!"}])
                   (rf/dispatch [:slack->send-message->user {:slackMessage "You have registered successfully your user with slack hoop.dev app."
                                                             :slackTeamId (first (cs/split (:slack-id user) #"-"))
                                                             :slackUserId (second (cs/split (:slack-id user) #"-"))}]))]
     {:fx [[:dispatch [:fetch
                       {:method "PATCH"
                        :uri (str "/users/self/slack")
                        :body {:slack_id (:slack-id user)}
                        :on-success success}]]]})))

(rf/reg-event-fx
 :users->get-user-groups
 (fn
   [{:keys [db]} [_]]
   {:fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/users/groups"
                      :on-success (fn [groups]
                                    (rf/dispatch [::users->set-user-groups groups]))
                      :on-failure #(println "Not allowed to get Users groups")}]]]}))

(rf/reg-event-fx
 ::users->set-user-groups
 (fn
   [{:keys [db]} [_ groups]]
   {:db (assoc db :user-groups groups)}))
