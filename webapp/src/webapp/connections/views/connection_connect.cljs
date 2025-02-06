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
   [:div {:class "flex flex-col items-center gap-small justify-center h-full my-x-large"}
    [:div {:class "text-gray-700"}
     "The connection time ended."]
    [:div {:class "text-gray-700"}
     "Please, try to connect again."]]])

(defn- draggable-card-content [connection]
  [:<>
   [:div
    [:small {:class "text-gray-700"}
     "Connected to: "]
    [:small {:class "font-bold text-gray-700"}
     (:connection_name connection)]]
   [:div
    [:small {:class "text-gray-700"}
     "Type: "]
    [:small {:class "font-bold text-gray-700"}
     (:connection_subtype connection)]]
   [:div
    [timer/main
     (.getTime (new js/Date (:connected-at connection)))
     (quot (:access_duration connection) 1000000)
     (fn []
       (rf/dispatch [:connections->connection-disconnect])
       (rf/dispatch [:draggable-card->close])
       (rf/dispatch [:modal->open {:content [disconnect-end-time]
                                   :maxWidth "446px"}]))]]])

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
        dialog-text "Are you sure you want to disconnect this connection?"
        open-dialog #(rf/dispatch [:dialog->open {:text dialog-text
                                                  :type :danger
                                                  :action-button? true
                                                  :on-success (fn []
                                                                (rf/dispatch [:connections->connection-disconnect])
                                                                (rf/dispatch [:draggable-card->close])
                                                                (rf/dispatch [:modal->close]))
                                                  :text-action-button "Disconnect"}])]
    (if (= (:status connection) :ready)
      (open-dialog)
      (rf/dispatch [:modal->close]))))

(defn- connect-credentials [connection]
  (case (:connection_subtype connection)
    "postgres" [:div {:class "my-regular"}
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
                  :logs "(none)"}]
                [:div {:class "font-bold text-xs pb-2"}
                 "SSL Mode"]
                [logs/container
                 {:status :success
                  :id "ssl-mode"
                  :logs "disable"}]]
    "mongodb" [:div {:class "my-regular"}
               [:div {:class "font-bold text-xs pb-2"}
                "Connection String"]
               [logs/container
                {:status :success
                 :id "connection-string"
                 :logs (str "mongodb://noop:noop@127.0.0.1:"
                            (:port connection)
                            "/?directConnection=true")}]]
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
       :logs "(none)"}]]))

(defn- connect-informations [connection]
  [:section
   [:header {:class "mb-4"}
    [:> Heading {:size "6" :as "h2"}
     "Hoop Access"]]
   [:main {:class "space-y-radix-1"}
    [:div
     [:small {:class "text-gray-700"}
      "Connection established with: "]
     [:small {:class "font-bold text-xs text-gray-700"}
      (:connection_name connection)]]
    [:div
     [:small {:class "text-gray-700"}
      "Type: "]
     [:small {:class "font-bold text-gray-700"}
      (:connection_subtype connection)]]
    [:div
     [timer/main
      (.getTime (new js/Date (:connected-at connection)))
      (quot (:access_duration connection) 1000000)
      (fn []
        (rf/dispatch [:connections->connection-disconnect])
        (rf/dispatch [:draggable-card->close])
        (rf/dispatch [:modal->open {:content [disconnect-end-time]
                                    :maxWidth "446px"}]))]]]

   [connect-credentials connection]

   [:div {:class "flex justify-end gap-3"}
    [button/secondary {:text "Minimize"
                       :outlined true
                       :on-click #(minimize-modal)}]
    [button/red-new {:text "Disconnect"
                     :on-click #(close-connect-dialog)}]]])

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
  (let [connection-data (-> connection :data)]
    [:main
     [:header {:class "mb-2"}
      [:> Heading {:size "6" :as "h2"}
       "Hoop Access"]]

     [:section {:class "my-regular"}
      [:span {:class "text-sm text-gray-800"}
       "Download and open Hoop Desktop App to have direct access to this connection."]]

     [:section {:class "flex justify-end gap-3"}
      [:> Button {:size "2"
                  :variant "outline"
                  :color "gray"
                  :on-click #(js/window.open "https://install.hoop.dev")}
       [:> Download {:size 16}]
       " Download Desktop App"]
      [:> Button {:size "2"
                  :on-click (fn []
                              (rf/dispatch [:connections->start-connect (:connection_name connection-data)]))}
       "Try again"]]]))

(defn main [connection-name]
  (let [connection @(rf/subscribe [:connections->connection-connected])]
    (case (:status connection)
      :ready [connect-informations (:data connection)]
      :loading [loading]
      :failure [failure connection connection-name])))
