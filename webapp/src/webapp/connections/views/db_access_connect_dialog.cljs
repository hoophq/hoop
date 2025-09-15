(ns webapp.connections.views.db-access-connect-dialog
  (:require
   ["@radix-ui/themes" :refer [Button Heading Tabs]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.button :as button]
   [webapp.components.logs-container :as logs]
   [webapp.components.timer :as timer]))

(defn- connect-credentials
  "Render credentials tab content"
  [db-access-data]
  [:div {:class "space-y-4"}

   ;; Database Name
   [:div
    [:div {:class "font-bold text-xs pb-2 text-gray-700"}
     "Database Name"]
    [logs/container
     {:status :success
      :id "database-name"
      :logs (:database_name db-access-data)}]]

   ;; Host
   [:div
    [:div {:class "font-bold text-xs pb-2 text-gray-700"}
     "Host"]
    [logs/container
     {:status :success
      :id "hostname"
      :logs (:hostname db-access-data)}]]

   ;; Username
   [:div
    [:div {:class "font-bold text-xs pb-2 text-gray-700"}
     "Username"]
    [logs/container
     {:status :success
      :id "username"
      :logs (:username db-access-data)}]]

   ;; Password
   [:div
    [:div {:class "font-bold text-xs pb-2 text-gray-700"}
     "Password"]
    [logs/container
     {:status :success
      :id "password"
      :logs (:password db-access-data)}]]

   ;; Port
   [:div
    [:div {:class "font-bold text-xs pb-2 text-gray-700"}
     "Port"]
    [logs/container
     {:status :success
      :id "port"
      :logs (:port db-access-data)}]]])

(defn- connect-uri
  "Render connection URI tab content"
  [db-access-data]
  [:div {:class "space-y-4"}
   [:div
    [:div {:class "font-bold text-xs pb-2 text-gray-700"}
     "Connection String"]
    [logs/container
     {:status :success
      :id "connection-string"
      :logs (:connection_string db-access-data)}]]

   [:div {:class "text-sm text-gray-600"}
    "Works with DBeaver, DataGrip and most PostgreSQL clients"]])

(defn- session-info
  "Render session information"
  [db-access-data]

  [:div {:class "space-y-2 mb-4"}
   [:div
    [:small {:class "text-gray-700"}
     "Connected to: "]
    [:small {:class "font-bold text-gray-700"}
     (:database_name db-access-data)]]

   [:div
    [timer/session-timer
     {:expire-at (:expire_at db-access-data)
      :on-session-end (fn []
                        (rf/dispatch [:db-access->clear-session])
                        (rf/dispatch [:modal->close])
                        (rf/dispatch [:show-snackbar {:level :info
                                                      :text "Database access session has expired."}]))}]]])

(defn minimize-modal
  "Minimize modal to draggable card"
  []

  (let [db-access-data @(rf/subscribe [:db-access->current-session])]
    (rf/dispatch [:modal->close])
    (when db-access-data
      (rf/dispatch [:draggable-card->open
                    {:component [:div {:class "p-4 min-w-64"}
                                 [:div {:class "space-y-2"}
                                  [:div
                                   [:small {:class "text-gray-700"}
                                    "Connected to: "]
                                   [:small {:class "font-bold text-gray-700"}
                                    (:database_name db-access-data)]]
                                  [:div
                                   [:small {:class "text-gray-700"}
                                    "Type: "]
                                   [:small {:class "font-bold text-gray-700"}
                                    "postgresql"]]
                                  [timer/session-timer
                                   {:expire-at (:expire_at db-access-data)
                                    :on-session-end (fn []
                                                      (rf/dispatch [:db-access->clear-session])
                                                      (rf/dispatch [:draggable-card->close])
                                                      (rf/dispatch [:show-snackbar {:level :info
                                                                                    :text "Database access session has expired."}]))}]]]
                     :on-click-expand (fn []
                                        (rf/dispatch [:draggable-card->close])
                                        (rf/dispatch [:db-access->reopen-connect-modal]))}]))))

(defn- close-connect-dialog
  "Handle disconnect with confirmation"
  []

  (let [dialog-text "Are you sure you want to disconnect this database session?"
        open-dialog #(rf/dispatch [:dialog->open {:text dialog-text
                                                  :type :danger
                                                  :action-button? true
                                                  :on-success (fn []
                                                                (rf/dispatch [:db-access->clear-session])
                                                                (rf/dispatch [:draggable-card->close])
                                                                (rf/dispatch [:modal->close]))
                                                  :text-action-button "Disconnect"}])]
    (open-dialog)))

(defn main
  "Main database access connect dialog"
  []

  (let [active-tab (r/atom "credentials")
        db-access-data (rf/subscribe [:db-access->current-session])
        session-valid? (rf/subscribe [:db-access->session-valid?])]

    (fn []
      (println @session-valid?)
      (if-not @session-valid?
        ;; Session invalid or expired
        [:section
         [:header {:class "mb-4"}
          [:> Heading {:size "6" :as "h2"}
           "Hoop Access"]]
         [:div {:class "text-center py-8"}
          [:p {:class "text-gray-600 mb-4"}
           "Your database access session has expired or is invalid."]
          [:> Button
           {:on-click #(rf/dispatch [:modal->close])}
           "Close"]]]

        ;; Valid session
        [:section
         [:header {:class "mb-4"}
          [:> Heading {:size "6" :as "h2"}
           "Connect to your Resource"]
          [:p {:class "text-sm text-gray-600 mt-1"}
           "Choose your preferred connection method below."]]

         [session-info @db-access-data]

         [:> Tabs.Root
          {:value @active-tab
           :onValueChange #(reset! active-tab %)}
          [:> Tabs.List {:aria-label "Connection methods"}
           [:> Tabs.Trigger {:value "credentials"} "Credentials"]
           [:> Tabs.Trigger {:value "connection-uri"} "Connection URI"]]

          [:> Tabs.Content {:value "credentials" :class "mt-4"}
           [connect-credentials @db-access-data]]

          [:> Tabs.Content {:value "connection-uri" :class "mt-4"}
           [connect-uri @db-access-data]]]

         ;; Info banner
         [:div {:class "bg-blue-50 border border-blue-200 rounded-lg p-3 mt-4"}
          [:div {:class "flex items-start gap-2"}
           [:div {:class "text-blue-600 text-sm"}
            "ⓘ"]
           [:div {:class "text-blue-800 text-sm"}
            [:span "These credentials are valid for "]
            [timer/inline-timer
             {:expire-at (:expire_at @db-access-data)}]
            [:span " starting now"]]]]

         ;; Actions
         [:footer {:class "flex justify-end gap-3 mt-6"}
          [button/secondary {:text "Minimize"
                             :outlined true
                             :on-click minimize-modal}]
          [button/red-new {:text "Disconnect"
                           :on-click close-connect-dialog}]]]))))
