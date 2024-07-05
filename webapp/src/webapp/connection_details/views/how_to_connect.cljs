(ns webapp.connection-details.views.how-to-connect
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.divider :as divider]
            [webapp.components.loaders :as loaders]
            [webapp.components.logs-container :as logs]))
(defn- reduce-plugins [connection-name my-plugins]
  (reduce
   (fn [array plugin]
     (if (some #(= connection-name (:name %)) (:connections plugin))
       (conj array (:name plugin))
       array))
   []
   my-plugins))

(defn circle-text
  [text]
  [:div {:class (str "flex items-center justify-center "
                     "rounded-full overflow-hidden w-5 h-5 "
                     "text-xs font-bold text-white bg-gray-800")}
   [:span text]])

(defmulti third-step-title identity)
(defmethod third-step-title :exec [_]
  "Exec your connection.")
(defmethod third-step-title :connect [_]
  "Connect your connection.")
(defmethod third-step-title :both [_]
  "Connect or Exec your connection.")

(defmulti third-step-logs identity)
(defmethod third-step-logs :exec [_ connection-name]
  [logs/container
   {:status :success
    :id "exec-step"
    :logs (str "hoop exec " connection-name)} ""])
(defmethod third-step-logs :connect [_ connection-name]
  [logs/container
   {:status :success
    :id "connect-step"
    :logs (str "hoop connect " connection-name)} ""])
(defmethod third-step-logs :both [_ connection-name]
  [:<>
   [logs/container
    {:status :success
     :id "connect-step"
     :logs (str "hoop connect " connection-name)} ""]
   [logs/container
    {:status :success
     :id "exec-step"
     :logs (str "hoop exec " connection-name)} ""]])

(defn connection-informations []
  (fn [{:keys [connection-name plugins user]}]
    (let [user-demo? (= (:id user) "test-user")
          connection-plugins (reduce-plugins @connection-name plugins)
          connection-plugins-filtered (filter #(or (= "jit" %)
                                                   (= "review" %))
                                              connection-plugins)
          run-type (cond
                     (> (count connection-plugins-filtered) 1) :both
                     (some #(= "jit" %) connection-plugins) :connect
                     (some #(= "review" %) connection-plugins) :exec
                     :else :both)]
      [:<>
       [divider/main]

       [:section {:class "my-regular"}
        [:div
         [:div {:class "mt-small"}
          (when-not user-demo?
            [:<>
             [:div {:class "flex gap-small items-center"}
              [circle-text "1"]
              [:label {:class "text-xs text-gray-700"}
               "Install hoop in your CLI."]]
             [logs/container
              {:status :success
               :id "install-step"
               :logs (str "brew tap hoophq/hoopcli https://github.com/hoophq/hoopcli\nbrew install hoop")} ""]
             [:div {:class "flex gap-small items-center"}
              [circle-text "2"]
              [:label {:class "text-xs text-gray-700"}
               "Login to Hoop."]]
             [logs/container
              {:status :success
               :id "login-step"
               :logs "hoop login"} ""]
             [:div {:class "flex gap-small items-center"}
              [circle-text "3"]
              [:label {:class "text-xs text-gray-700"}
               (third-step-title run-type)]]])
          [third-step-logs run-type @connection-name]]]]])))

(defn container
  [connection plugins user]
  (let [connection-name (r/atom (or (:name connection) ""))]
    (fn [_ _]
      [:main
       [connection-informations
        {:connection-name connection-name
         :plugins plugins
         :user (:data user)}]])))

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center"}
   [loaders/simple-loader]])

(defn main [connection]
  (let [user @(rf/subscribe [:users->current-user])
        my-plugins @(rf/subscribe [:plugins->my-plugins])]
    (if (true? (:loading connection))
      [loading-list-view]
      [container (:data connection) my-plugins user])))

