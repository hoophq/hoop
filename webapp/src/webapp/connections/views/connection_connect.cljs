(ns webapp.connections.views.connection-connect
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["@heroicons/react/16/solid" :as hero-micro-icon]
            [re-frame.core :as rf]
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
    [:> hero-outline-icon/ClockIcon {:class "h-4 w-4 text-gray-600"}]
    [:small {:class "text-gray-700"}
     [timer/main
      (.getTime (new js/Date (:connected-at connection)))
      (quot (:access_duration connection) 1000000)
      #(rf/dispatch [:open-modal [disconnect-end-time] :default])]]]
   [:div {:class "mt-6 grid grid-cols-1 gap-small"}
    [:div {:class "sm:col-span-1"}
     [:span {:class "text-sm font-medium text-gray-700"}
      "Port: "]
     [:span {:class "mt-1 text-sm text-gray-700 font-bold"}
      (:port connection)]]
    [:div {:class "sm:col-span-1"}
     [:span {:class "text-sm font-medium text-gray-700"}
      "HostName: "]
     [:span {:class "mt-1 text-sm text-gray-700 font-bold"}
      "localhost"]]
    [:div {:class "sm:col-span-1"}
     [:span {:class "text-sm font-medium text-gray-700"}
      "User: "]
     [:span {:class "mt-1 text-sm text-gray-700 font-bold"}
      "(none)"]]
    [:div {:class "sm:col-span-1"}
     [:span {:class "text-sm font-medium text-gray-700"}
      "Password: "]
     [:span {:class "mt-1 text-sm text-gray-700 font-bold"}
      "(none)"]]]])

(defn- connect-informations [connection]
  [:section {:class "p-2"}
   [:header {:class "mb-2"}
    [h/h3 "Hoop access details"]]
   [:main
    [:div {:class "flex items-center gap-small py-3"}
     [:> hero-micro-icon/CheckIcon {:class "h-4 w-4 text-green-500"}]
     [:span {:class "text-xs text-gray-700"}
      "Connection established with:"]
     [:span {:class "font-bold text-xs text-gray-700"}
      (:connection_name connection)]]
    [:div {:class "flex items-center gap-small"}
     [:> hero-outline-icon/ClockIcon {:class "h-4 w-4 text-gray-600"}]
     [:span {:class "text-xs text-gray-600"}
      [timer/main
       (.getTime (new js/Date (:connected-at connection)))
       (quot (:access_duration connection) 1000000)
       #(rf/dispatch [:draggable-card->open-modal [disconnect-end-time] :default])]]]

    [:div {:class "my-regular"}
     [:div {:class "font-bold text-xs pb-2"}
      "Hostname"]
     [logs/container
      {:status :success
       :id "hostname"
       :logs "localhost"}]
     [:div {:class "font-bold text-xs pb-2"}
      "Port"]
     [logs/container
      {:status :success
       :id "port"
       :logs (:port connection)}]
     [:div {:class "font-bold text-xs pb-2"}
      "Username"]
     [logs/container
      {:status :success
       :id "username"
       :logs "(none)"}]
     [:div {:class "font-bold text-xs pb-2"}
      "Password"]
     [logs/container
      {:status :success
       :id "password"
       :logs "(none)"}]]
    [:div {:class "flex justify-end gap-3"}
     [button/secondary {:text "Minimize"
                        :outlined true
                        :on-click #(minimize-modal draggable-card-content)}]
     [button/red-new {:text "Disconnect"
                      :on-click #(close-connect-dialog)}]]]])

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

(defn- failure [error connection-name]
  [:main {:class "p-2"}
   [:header {:class "mb-2"}
    [h/h3 "Hoop access details"]]

   [:section {:class "flex gap-small"}
    [:div {:class "flex items-center gap-small my-small"}
     [:> hero-micro-icon/XMarkIcon {:class "h-4 w-4 text-red-500"}]
     [:span {:class "text-xs text-gray-700"}
      "Failed attempt to connect to:"]
     [:span {:class "font-bold text-xs text-gray-600"}
      (:connection_name (:data error))]]]

   [:section {:class "my-regular"}
    [:span {:class "text-sm text-gray-800"}
     "Make sure hoop app is open and running with a valid account logged in."]
    [:span {:class "block text-nowrap text-sm text-gray-800"}
     "You can also download hoop.dev desktop app and setup your hoop access."]]

   [:section {:class "flex justify-end gap-3"}
    [button/tailwind-secondary {:text "Setup hoop access"
                                :outlined? true
                                :on-click (fn []
                                            (js/clearTimeout)
                                            (rf/dispatch [:connections->open-connect-setup connection-name]))}]

    [button/tailwind-primary {:text [:div {:class "flex gap-2"}
                                     [:> hero-outline-icon/SignalIcon {:class "h-5 w-5"}]
                                     "Try again"]
                              :outlined? true
                              :on-click (fn []
                                          (rf/dispatch [:connections->connection-connect connection-name]))}]]])

(defn main [connection-name]
  (let [connection @(rf/subscribe [:connections->connection-connected])]
    (case (:status connection)
      :ready [connect-informations (:data connection)]
      :loading [loading connection]
      :failure [failure connection connection-name])))

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
