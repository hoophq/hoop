(ns webapp.events.auth
  (:require
   [re-frame.core :as rf]
   [webapp.routes :as routes]))

(rf/reg-event-fx
 :auth->get-auth-link
 (fn [_ [_ {:keys [prompt-login?]}]]
   (let [on-success #(.replace js/window.location (:login_url %))
         base-uri (if prompt-login? "/login?prompt=login&redirect=" "/login?redirect=")
         get-email [:fetch {:method "GET"
                            :uri (str base-uri
                                      (str (. (. js/window -location) -origin)
                                           (routes/url-for :auth-callback-hoop)))
                            :on-success on-success}]]
     {:fx [[:dispatch get-email]]})))

(rf/reg-event-fx
 :auth->get-signup-link
 (fn []
   (let [on-success #(.replace js/window.location (:login_url %))
         get-email [:fetch {:method "GET"
                            :uri (str "/login?prompt=login&screen_hint=signup&redirect="
                                      (str (. (. js/window -location) -origin)
                                           (routes/url-for :signup-callback-hoop)))
                            :on-success on-success}]]
     {:fx [[:dispatch get-email]]})))

(rf/reg-event-fx
 :auth->signup
 (fn [_ [_ org-name profile-name]]
   {:fx [[:dispatch [:fetch {:method "POST"
                             :uri "/signup"
                             :body (merge
                                    (when-not (empty? profile-name)
                                      {:profile_name profile-name})
                                    {:org_name org-name})
                             :on-success (fn [_]
                                           (rf/dispatch [:organization->create-api-key])
                                           (rf/dispatch [:navigate :home]))
                             :on-failure (fn [error]
                                           (println error))}]]]}))

(rf/reg-event-fx
 :auth->logout
 (fn [{:keys [db]} [_ {:keys [idp?]}]]
   (let [auth0-logout-url (str "https://hoophq.us.auth0.com"
                               "/v2/logout?"
                               "client_id=DatIOCxntNv8AZrQLVnLb3tr1Y3oVwGW"
                               "&returnTo=" (js/encodeURIComponent
                                             (str (. (. js/window -location) -origin)
                                                  (routes/url-for :login-hoop)))
                               "&federated")]

     (if (= (get-in db [:users->current-user :data :tenancy_type]) "multitenant")
       (do
         (set! (.. js/window -location -href) auth0-logout-url)
         {:db {}})

       {:db {}
        :navigate {:handler :logout-hoop}}))))
