(ns webapp.connections.views.db-access-connect-dialog
  (:require
   ["@radix-ui/themes" :refer [Button Heading Tabs Text Box]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.logs-container :as logs]
   [webapp.components.timer :as timer]))

(defn- connect-credentials
  "Render credentials tab content"
  [db-access-data]
  [:> Box {:class "space-y-4"}

   ;; Database Name
   [:> Box {:class "space-y-3"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Database Name"]
    [logs/new-container
     {:status :success
      :id "database-name"
      :logs (:database_name db-access-data)}]]

   ;; Host
   [:> Box {:class "space-y-3"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Host"]
    [logs/new-container
     {:status :success
      :id "hostname"
      :logs (:hostname db-access-data)}]]

   ;; Username
   [:> Box {:class "space-y-3"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Username"]
    [logs/new-container
     {:status :success
      :id "username"
      :logs (:username db-access-data)}]]

   ;; Password
   [:> Box {:class "space-y-3"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Password"]
    [logs/new-container
     {:status :success
      :id "password"
      :logs (:password db-access-data)}]]

   ;; Port
   [:> Box {:class "space-y-3"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Port"]
    [logs/new-container
     {:status :success
      :id "port"
      :logs (:port db-access-data)}]]])

(defn- connect-uri
  "Render connection URI tab content"
  [db-access-data]
  [:> Box {:class "space-y-4"}
   [:> Box {:class "space-y-3"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Connection String"]
    [logs/new-container
     {:status :success
      :id "connection-string"
      :logs (:connection_string db-access-data)}]]

   [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
    "Works with DBeaver, DataGrip and most PostgreSQL clients"]])

(defn minimize-modal
  "Minimize modal to draggable card"
  []

  (let [db-access-data @(rf/subscribe [:db-access->current-session])]
    (rf/dispatch [:modal->close])
    (when db-access-data
      (rf/dispatch [:draggable-card->open
                    {:component [:> Box {:class "p-4 min-w-64"}
                                 [:> Box {:class "space-y-2"}
                                  [:> Box
                                   [:small {:class "text-gray-700"}
                                    "Connected to: "]
                                   [:small {:class "font-bold text-gray-700"}
                                    (:database_name db-access-data)]]
                                  [:> Box
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
      (if-not @session-valid?
        ;; Session invalid or expired
        [:section
         [:header {:class "mb-4"}
          [:> Heading {:size "6" :as "h2"}
           "Hoop Access"]]
         [:> Box {:class "text-center py-8"}
          [:p {:class "text-gray-600 mb-4"}
           "Your database access session has expired or is invalid."]
          [:> Button
           {:on-click #(rf/dispatch [:modal->close])}
           "Close"]]]

        ;; Valid session
        [:section {:class "space-y-radix-8"}
         [:header {:class "space-y-radix-2"}
          [:> Heading {:size "8" :as "h2"}
           (str "Connect to " (:connection_name @db-access-data))]
          [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
           "Connectino established, time left: "

           [timer/inline-timer
            {:expire-at (:expire_at @db-access-data)
             :urgent-threshold 60000
             :on-complete (fn []
                            (rf/dispatch [:db-access->clear-session])
                            (rf/dispatch [:modal->close])
                            (rf/dispatch [:show-snackbar {:level :info
                                                          :text "Database access session has expired."}]))}]]]

         [:> Tabs.Root {:value @active-tab
                        :onValueChange #(reset! active-tab %)}
          [:> Tabs.List {:aria-label "Connection methods"}
           [:> Tabs.Trigger {:value "credentials"} "Credentials"]
           [:> Tabs.Trigger {:value "connection-uri"} "Connection URI"]]

          [:> Tabs.Content {:value "credentials" :class "mt-4"}
           [connect-credentials @db-access-data]]

          [:> Tabs.Content {:value "connection-uri" :class "mt-4"}
           [connect-uri @db-access-data]]]

         ;; Actions
         [:footer {:class "flex justify-between items-center gap-3 mt-6"}
          [:> Button
           {:variant "ghost"
            :size "3"
            :color "gray"
            :class "ml-0"
            :on-click minimize-modal}
           "Minimize"]
          [:> Button
           {:variant "solid"
            :size "3"
            :color "red"
            :on-click close-connect-dialog}
           "Disconnect"]]]))))
