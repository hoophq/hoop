(ns webapp.connections.db-access.main
  "Integrated Database Access Flow

  This component provides a unified experience for database access sessions,
  combining both the duration selection and connection details in a single modal.

  Flow:
  1. User clicks 'Open in Native Client' from connection list
  2. Modal opens showing duration selection form (configure-session-view)
  3. User selects duration and clicks 'Confirm and Connect'
  4. Backend validates and creates session
  5. Component transitions to connection details (connection-established-view)
  6. User can view credentials in tabs (Credentials/Connection URI)
  7. User can minimize to draggable card or disconnect session

  Key Features:
  - Seamless transition between configuration and connection states
  - Session timer with expiration handling
  - Consistent UI following the features/ pattern
  - Uses lookup functions from db-access constants for duration display
  - Supports minimize to draggable card for multi-tasking

  Integration Points:
  - Event: :db-access->start-flow [connection] - Opens this component
  - Event: :db-access->request-access [connection duration] - Requests backend access
  - Event: :db-access->clear-session - Cleans up current session
  - Subscriptions: :db-access->current-session, :db-access->session-valid?
  "
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Tabs Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.logs-container :as logs]
   [webapp.components.timer :as timer]
   [webapp.connections.constants.db-access :as db-access-constants]))

(defn- configure-session-view
  "Step 1: Configure session duration"
  [connection-name selected-duration requesting?]
  [:> Flex {:direction "column" :justify "between" :gap "8" :class "h-full"}
   [:> Box {:class "space-y-8"}
    [:header {:class "mb-6"}
     [:> Heading {:size "6" :as "h2" :class "text-[--gray-12] mb-2"}
      "Configure session"]
     [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
      "Specify how long you need access to this connection."]]

    [:> Box {:class "space-y-4"}
     [:> Box
      [:> Text {:as "label" :size "2" :weight "bold" :class "text-[--gray-12] mb-2"}
       "Access duration"]
      [forms/select
       {:size "2"
        :not-margin-bottom? true
        :placeholder "Select duration"
        :on-change #(reset! selected-duration (js/parseInt %))
        :selected @selected-duration
        :full-width? true
        :options db-access-constants/access-duration-options}]]

     [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
      "Your access will automatically expire after this period"]]]

   [:footer {:class "flex justify-end gap-3 mt-8"}
    [:> Button
     {:variant "solid"
      :size "3"
      :loading @requesting?
      :disabled @requesting?
      :on-click #(rf/dispatch [:db-access->request-access
                               connection-name
                               @selected-duration])}
     "Confirm and Connect"]]])

(defn- connect-credentials-tab
  "Credentials tab content"
  [db-access-data]
  [:> Box {:class "space-y-4"}

   ;; Database Name
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Database Name"]
    [logs/new-container
     {:status :success
      :id "database-name"
      :logs (:database_name db-access-data)}]]

   ;; Host
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Host"]
    [logs/new-container
     {:status :success
      :id "hostname"
      :logs (:hostname db-access-data)}]]

   ;; Username
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Username"]
    [logs/new-container
     {:status :success
      :id "username"
      :logs (:username db-access-data)}]]

   ;; Password
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Password"]
    [logs/new-container
     {:status :success
      :id "password"
      :logs (:password db-access-data)}]]

   ;; Port
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Port"]
    [logs/new-container
     {:status :success
      :id "port"
      :logs (:port db-access-data)}]]])

(defn- connect-uri-tab
  "Connection URI tab content"
  [db-access-data]
  [:> Box {:class "space-y-4"}
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Connection String"]
    [logs/new-container
     {:status :success
      :id "connection-string"
      :logs (:connection_string db-access-data)}]]

   [:> Text {:as "p" :size "2" :class "text-[--gray-11] mt-3"}
    "Works with DBeaver, DataGrip and most PostgreSQL clients"]])

(defn- connection-established-view
  "Step 2: Connection established - show credentials"
  [db-access-data minimize-fn disconnect-fn]
  (let [active-tab (r/atom "credentials")]

    (fn []
      [:<>
       [:> Box {:class "space-y-6"}
        [:header {:class "space-y-3"}
         [:> Heading {:size "6" :as "h2" :class "text-[--gray-12]"}
          (str "Connect to " (:connection_name db-access-data))]

         [:> Flex {:align "center" :gap "2"}
          [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
           "Connection established, time left: "]
          [timer/inline-timer
           {:expire-at (:expire_at db-access-data)
            :text-component (fn [timer-text]
                              [:> Text {:size "3" :weight "bold" :class "text-[--gray-11]"}
                               timer-text])
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
          [connect-credentials-tab db-access-data]]

         [:> Tabs.Content {:value "connection-uri" :class "mt-4"}
          [connect-uri-tab db-access-data]]]]

       ;; Actions
       [:footer {:class "flex justify-between items-center"}
        [:> Button
         {:variant "ghost"
          :size "3"
          :color "gray"
          :on-click minimize-fn}
         "Minimize"]
        [:> Button
         {:variant "solid"
          :size "3"
          :color "red"
          :on-click disconnect-fn}
         "Disconnect"]]])))

(defn- session-expired-view
  "Fallback view for expired sessions"
  []
  [:> Box {:class "text-center space-y-4 py-8"}
   [:> Heading {:size "5" :as "h2" :class "text-[--gray-12]"}
    "Session Expired"]
   [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
    "Your database access session has expired or is invalid."]
   [:> Button
    {:variant "solid"
     :size "3"
     :on-click #(rf/dispatch [:modal->close])}
    "Close"]])

(defn minimize-modal-content [db-access-data]
  [:> Box {:class "min-w-32"}
   [:> Box {:class "space-y-2"}
    [:> Box
     [:> Text {:size "2" :class "text-[--gray-12]"}
      "Connected to: "]
     [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
      (:database_name db-access-data)]]
    [:> Box
     [:> Text {:size "2" :class "text-[--gray-12]"}
      "Type: "]
     [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
      "postgresql"]]
    [:> Box
     [:> Text {:size "2" :class "text-[--gray-12]"}
      "Time left: "]
     [timer/inline-timer
      {:expire-at (:expire_at db-access-data)
       :text-component (fn [timer-text]
                         [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
                          timer-text])
       :on-complete (fn []
                      (rf/dispatch [:db-access->clear-session])
                      (rf/dispatch [:draggable-card->close])
                      (rf/dispatch [:show-snackbar {:level :info
                                                    :text "Database access session has expired."}]))}]]]])

(defn minimize-modal
  "Minimize modal to draggable card"
  []
  (let [db-access-data @(rf/subscribe [:db-access->current-session])]
    (rf/dispatch [:modal->close])
    (when db-access-data
      (rf/dispatch [:draggable-card->open
                    {:component [minimize-modal-content db-access-data]
                     :on-click-expand (fn []
                                        (rf/dispatch [:draggable-card->close])
                                        (rf/dispatch [:db-access->reopen-connect-modal]))}]))))

(defn disconnect-session
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
  "Main database access component - manages the complete flow"
  [connection-name]
  (let [selected-duration (r/atom 30)
        requesting? (rf/subscribe [:db-access->requesting?])
        db-access-data (rf/subscribe [:db-access->current-session])
        session-valid? (rf/subscribe [:db-access->session-valid?])]

    [:> Box {:class "flex max-h-[696px] overflow-hidden -m-radix-5"}
     [:> Flex {:direction "column" :justify "between" :gap "6" :class "w-full p-10 overflow-y-auto"}
      ;; Main content based on current state
      (cond
        ;; Step 2: Connected - show connection details
        (and @session-valid? @db-access-data)
        [connection-established-view @db-access-data minimize-modal disconnect-session]

        ;; Step 1: Configure session duration
        (not @db-access-data)
        [configure-session-view connection-name selected-duration requesting?]

        ;; Fallback: Session expired
        :else
        [session-expired-view])]

     [:> Box {:class "min-w-[525px] bg-blue-50 max-h-[696px]"}
      [:img {:src "/images/illustrations/cli-promotion.png"
             :alt  "cli illustration"
             :class "w-full h-full object-cover"}]]]))
