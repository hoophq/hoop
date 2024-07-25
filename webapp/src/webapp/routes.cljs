(ns webapp.routes
  (:require [bidi.bidi :as bidi]
            [pushy.core :as pushy]
            [re-frame.core :as re-frame]
            [webapp.config :as config]
            [webapp.events :as events]))

(defmulti panels identity)

(def routes
  (atom
   ["/" {"" :home
         "404" :404
         "auth/callback" :auth-callback-hoop
         "connections" [["" :connections]
                        ["/details" :connection-details]
                        ["/new" :create-connection]
                        [["/connections/" :connection-type "/new"] :onboarding-create-connection]]
         "client" :editor-plugin
         "hoop-app" :hoop-app
         "login" :login-hoop
         "logout" :logout-hoop
         "organization" [["/users" :users]]
         "plugins" [["/manage/ask-ai" :manage-ask-ai]
                    [["/reviews/" :review-id] :reviews-plugin-details]
                    [["/manage/" :plugin-name] :manage-plugin]]
         "register" :register-hoop
         "reviews" :reviews-plugin
         "runbooks" [["" :runbooks-plugin]
                     [["/" :runbooks-file] :runbooks-plugin]]
         "slack" [[["/user" "/new/" :slack-id] :slack-new-user]
                  [["/organization" "/new"] :slack-new-organization]]
         "sessions" [["" :sessions]
                     ["/filtered" :sessions-list-filtered-by-ids]
                     [["/" :session-id] :session-details]]
         "signup" :signup-hoop
         "signup/callback" :signup-callback-hoop}]))

(defn query-params-parser
  [queries]
  (let [url-search-params (new js/URLSearchParams (clj->js queries))]
    (if (not (empty? (.toString url-search-params)))
      (str "?" (.toString url-search-params))
      "")))

(defn parse
  [url]
  (bidi/match-route @routes url))

(defn url-for
  [& args]
  (apply bidi/path-for (into [@routes] (flatten args))))

(defn dispatch
  [route]
  (let [panel (keyword (str (name (:handler route)) "-panel"))]
    (re-frame/dispatch [::events/set-active-panel panel])))

(defonce history
  (pushy/pushy dispatch parse))

(defn navigate!
  [config]
  (let [uri (str (url-for (:handler config) (or (:params config) []))
                 (:query-params config))]
    (pushy/set-token! history uri)))

(defn start!
  []
  (pushy/start! history))

(re-frame/reg-fx
 :navigate
 (fn [config]
   (navigate! {:handler (:handler config)
               :params (:params config)
               :query-params (query-params-parser (:query-params config))})))
