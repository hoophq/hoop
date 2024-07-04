(ns webapp.connections.views.connection-connect
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.divider :as divider]
            [webapp.components.headings :as h]
            [webapp.components.icon :as icon]
            [webapp.components.loaders :as loaders]
            [webapp.components.logs-container :as logs]
            [webapp.components.timer :as timer]
            [webapp.http.api :as api]
            [webapp.utilities :as utilities]))

(defn- circle-text
  [text]
  [:div {:class (str "flex items-center justify-center "
                     "rounded-full overflow-hidden w-5 h-5 "
                     "text-xs font-bold text-white bg-gray-800")}
   [:span text]])

(defn- close-all-events []
  (rf/dispatch [:connections->connection-disconnect])
  (rf/dispatch [:draggable-card->close])
  (rf/dispatch [:draggable-card->close-modal]))

(defn- minimize-modal [draggable-card-component]
  (let [connection @(rf/subscribe [:connections->connection-connected])]
    (utilities/add-class!
     (-> js/document
         (.getElementById "modal-draggable-card"))
     "hidden")
    (rf/dispatch [:draggable-card->open
                  {:component [draggable-card-component (:data connection)]
                   :on-click-expand (fn []
                                      (rf/dispatch [:draggable-card->close])
                                      (utilities/remove-class!
                                       (-> js/document
                                           (.getElementById "modal-draggable-card"))
                                       "hidden"))}])))

(defn- close-connect-dialog []
  (let [connection @(rf/subscribe [:connections->connection-connected])
        dialog-text (str "Are you sure you want to disconnect this connection?")
        open-dialog #(rf/dispatch [:dialog->open {:text dialog-text
                                                  :type :danger
                                                  :on-success close-all-events
                                                  :text-action-button "Disconnect"}])]
    (if (= (:status connection) :ready)
      (open-dialog)
      (rf/dispatch [:draggable-card->close-modal]))))

(defn- disconnect-end-time []
  (close-all-events)
  [:section
   [:div {:class "max-w-7xl px-4"}
    [:div {:class "flex flex-col items-center gap-regular justify-center h-full my-x-large"}
     [:div {:class "flex gap-small"}
      [:span {:class "text-gray-700"}
       "Your time with this connection has ended, please connect to a new connection."]]]]])

(defn- draggable-card-content [connection]
  [:<>
   [:small {:class "text-gray-700"}
    "Connected to: "]
   [:small {:class "font-bold text-gray-700"}
    (:connection_name connection)]
   [:div {:class "flex items-center gap-small my-small"}
    [icon/regular {:icon-name "watch-black"
                   :size 6}]
    [:small {:class "font-bold text-gray-700"}
     [timer/main
      (.getTime (new js/Date (:connected-at connection)))
      (quot (:access_duration connection) 1000000)
      #(rf/dispatch [:open-modal [disconnect-end-time] :default])]]]
   [:div {:class "mt-2 grid grid-cols-1 gap-small"}
    [:div {:class "sm:col-span-1"}
     [:span {:class "text-sm font-medium text-gray-500"}
      "Port: "]
     [:span {:class "mt-1 text-sm text-gray-900"}
      (:port connection)]]
    [:div {:class "sm:col-span-1"}
     [:span {:class "text-sm font-medium text-gray-500"}
      "HostName: "]
     [:span {:class "mt-1 text-sm text-gray-900"}
      "localhost"]]
    [:div {:class "sm:col-span-1"}
     [:span {:class "text-sm font-medium text-gray-500"}
      "User: "]
     [:span {:class "mt-1 text-sm text-gray-900"}
      "(none)"]]
    [:div {:class "sm:col-span-1"}
     [:span {:class "text-sm font-medium text-gray-500"}
      "Password: "]
     [:span {:class "mt-1 text-sm text-gray-900"}
      "(none)"]]]])

(defn- connect-informations [connection]
  [:section
   [:div {:class "max-w-7xl px-4"}
    [:main {:class "mt-large"}
     [:div {:class "flex items-center gap-small my-small"}
      [icon/regular {:icon-name "check-green"
                     :size 6}]
      [:span {:class "text-xs text-gray-600"}
       "Connection established with:"]
      [:span {:class "font-bold text-xs text-gray-600"}
       (:connection_name connection)]]
     [:div {:class "flex items-center gap-small my-small"}
      [icon/regular {:icon-name "watch-black"
                     :size 6}]
      [:span {:class "font-bold text-xs text-gray-600"}
       [timer/main
        (.getTime (new js/Date (:connected-at connection)))
        (quot (:access_duration connection) 1000000)
        #(rf/dispatch [:draggable-card->open-modal [disconnect-end-time] :default])]]]
     [divider/main]
     [h/h3 "Informations"]
     [:div {:class "my-regular"}
      [:span {:class "font-bold text-sm"}
       "Hostname:"]
      [logs/container
       {:status :success
        :id "hostname"
        :logs "localhost"} ""]
      [:span {:class "font-bold text-sm"}
       "Port:"]
      [logs/container
       {:status :success
        :id "port"
        :logs (:port connection)} ""]
      [:span {:class "font-bold text-sm"}
       "Username:"]
      [logs/container
       {:status :success
        :id "username"
        :logs "(none)"} ""]
      [:span {:class "font-bold text-sm"}
       "Password:"]
      [logs/container
       {:status :success
        :id "password"
        :logs "(none)"} ""]]
     [:div {:class "grid grid-cols-2 gap-regular"}
      [button/red-new {:text "Close"
                       :full-width true
                       :on-click #(close-connect-dialog)}]
      [button/secondary {:text "Minimize"
                         :outlined true
                         :full-width true
                         :on-click #(minimize-modal draggable-card-content)}]]]]])

(defn- loading [connection]
  [:section
   [:div {:class "max-w-7xl px-4"}
    [:div {:class "flex flex-col items-center gap-regular justify-center h-full my-x-large"}
     [:div {:class "flex gap-small"}
      [loaders/simple-loader]
      [:span {:class "text-gray-600"}
       "Establishing connection with:"]]
     [:span {:class "font-bold text-gray-600"}
      (:name (:data connection))]]]])

(defn- failure [error]
  [:section
   [:main {:class "max-w-7xl px-4 mt-large"}
    [:section {:class "flex gap-small"}
     [:div {:class " flex items-center gap-small my-small"}
      [icon/regular {:icon-name "close-red"
                     :size 6}]
      [:span {:class "text-xs text-gray-700"}
       "Failed attempt to connect to:"]
      [:span {:class "font-bold text-xs text-gray-600"}
       (:connection_name (:data error))]]]
    [divider/main]
    [:section {:class "my-regular"}
     [:div
      [h/h4
       "How to connect in your connection:"]
      [:div {:class "mt-small"}
       [:<>
        [:div {:class "flex gap-small items-center"}
         [circle-text "1"]
         [:label {:class "text-xs text-gray-700"}
          "Login to Hoop."]]
        [logs/container
         {:status :success
          :id "login-step"
          :logs "hoop login"} ""]
        [:div {:class "flex gap-small items-center"}
         [circle-text "2"]
         [:label {:class "text-xs text-gray-700"}
          "Execute proxy manager."]]
        [logs/container
         {:status :success
          :id "install-step"
          :logs "hoop proxy-manager"} ""]]]]]
    [:span {:class "font-bold text-gray-600"}
     error.message]]])

(defn main []
  (let [connection @(rf/subscribe [:connections->connection-connected])]
    (case (:status connection)
      :ready [connect-informations (:data connection)]
      :loading [loading connection]
      :failure [failure connection])))

(defn handle-close-modal []
  (let [connection @(rf/subscribe [:connections->connection-connected])]
    (case (:status connection)
      :ready (minimize-modal draggable-card-content)
      :loading (println "is loading")
      :failure (rf/dispatch [:draggable-card->close-modal]))))

(defn verify-connection-status []
  (let [connection (r/atom nil)]
    (api/request {:method "GET"
                  :uri "/proxymanager/status"
                  :on-success (fn [res]
                                (reset! connection res))
                  :on-failure #(println :failure :connections->connection-get-status %)})
    (rf/dispatch [:connections->connection-get-status])
    (fn []
      (when (= (:status @connection) "connected")
        (rf/dispatch [:draggable-card->open-modal
                      [main]
                      :default
                      #(minimize-modal draggable-card-content)])))))
