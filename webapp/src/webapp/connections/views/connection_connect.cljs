(ns webapp.connections.views.connection-connect
  (:require ["@radix-ui/themes" :refer [Box Button Flex Heading Skeleton]]
            ["lucide-react" :refer [Download]]
            [re-frame.core :as rf]
            [webapp.components.button :as button]
            [webapp.components.logs-container :as logs]
            [webapp.components.timer :as timer]))

(defn- disconnect-end-time []
  [:section
   [:header {:class "mb-2"}
    [:> Heading {:size "6" :as "h2"}
     "Hoop Access"]]
   [:div {:class "flex flex-col items-center gap-regular justify-center h-full my-x-large"}
    [:div {:class "flex gap-small"}
     [:span {:class "text-gray-700"}
      "Your time with this connection has ended, please connect to a new connection."]]]])

(defn- draggable-card-content [connection]
  [:<>
   [:small {:class "text-gray-700"}
    "Connected to: "]
   [:small {:class "font-bold text-gray-700"}
    (:connection_name connection)]
   [:div {:class "flex items-center gap-small my-small"}
    [:small {:class "text-gray-700"}
     [timer/main
      (.getTime (new js/Date (:connected-at connection)))
      (quot (:access_duration connection) 1000000)
      (fn []
        (rf/dispatch [:connections->connection-disconnect])
        (rf/dispatch [:draggable-card->close])
        (rf/dispatch [:modal->open {:content [disconnect-end-time]
                                    :maxWidth "446px"}]))]]]
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

(defn minimize-modal []
  (let [connection @(rf/subscribe [:connections->connection-connected])]
    (rf/dispatch [:modal->close])
    (rf/dispatch [:draggable-card->open
                  {:component [draggable-card-content (:data connection)]
                   :on-click-expand (fn []
                                      (rf/dispatch [:draggable-card->close])
                                      (rf/dispatch [:modal->re-open]))}])))

(defn- close-connect-dialog []
  (let [connection @(rf/subscribe [:connections->connection-connected])
        dialog-text (str "Are you sure you want to disconnect this connection?")
        open-dialog #(rf/dispatch [:dialog->open {:text dialog-text
                                                  :type :danger
                                                  :on-success (fn []
                                                                (rf/dispatch [:connections->connection-disconnect])
                                                                (rf/dispatch [:draggable-card->close])
                                                                (rf/dispatch [:modal->close]))
                                                  :text-action-button "Disconnect"}])]
    (if (= (:status connection) :ready)
      (open-dialog)
      (rf/dispatch [:modal->close]))))

(defn- connect-informations [connection]
  [:section
   [:header {:class "mb-2"}
    [:> Heading {:size "6" :as "h2"}
     "Hoop Access"]]
   [:main
    [:div {:class "flex items-center gap-small py-3"}
     [:span {:class "text-xs text-gray-700"}
      "Connection established with:"]
     [:span {:class "font-bold text-xs text-gray-700"}
      (:connection_name connection)]]
    [:div {:class "flex items-center gap-small"}
     [:span {:class "text-xs text-gray-600"}
      [timer/main
       (.getTime (new js/Date (:connected-at connection)))
       (quot (:access_duration connection) 1000000)
       (fn []
         (rf/dispatch [:connections->connection-disconnect])
         (rf/dispatch [:draggable-card->close])
         (rf/dispatch [:modal->open {:content [disconnect-end-time]
                                     :maxWidth "446px"}]))]]]

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
                        :on-click #(minimize-modal)}]
     [button/red-new {:text "Disconnect"
                      :on-click #(close-connect-dialog)}]]]])

(defn- loading []
  [:> Flex {:direction "column" :gap "5"}
   [:> Box {:class "space-y-radix-3"}
    [:> Heading {:size "6" :as "h2"}
     "Hoop Access"]

    [:> Box {:class "space-y-radix-2"}
     [:> Skeleton {:height "20px"}]
     [:> Skeleton {:height "20px"}]]]

   [:> Box {:class "space-y-radix-3"}
    [:> Skeleton {:height "60px"}]
    [:> Skeleton {:height "60px"}]
    [:> Skeleton {:height "60px"}]
    [:> Skeleton {:height "60px"}]]])

(defn- failure [connection]
  [:main
   [:header {:class "mb-2"}
    [:> Heading {:size "6" :as "h2"}
     "Hoop Access"]]

   [:section {:class "my-regular"}
    [:span {:class "text-sm text-gray-800"}
     "Download and open Hoop Desktop App to enable a direct access to this connection."]]

   [:section {:class "flex justify-end gap-3"}
    [:> Button {:size "2"
                :variant "outline"
                :color "gray"
                :on-click #(js/window.open "https://install.hoop.dev")}
     [:> Download {:size 16}]
     " Download Desktop App"]
    [:> Button {:size "2"
                :on-click (fn []
                            (rf/dispatch [:connections->start-connect (-> connection :data :connection_name)]))}
     "Try again"]]])

(defn main [connection-name]
  (let [connection @(rf/subscribe [:connections->connection-connected])]
    (case (:status connection)
      :ready [connect-informations (:data connection)]
      :loading [loading]
      :failure [failure connection connection-name])))
