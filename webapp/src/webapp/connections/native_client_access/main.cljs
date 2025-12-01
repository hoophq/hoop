(ns webapp.connections.native-client-access.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Tabs Text]]
   ["lucide-react" :refer [Info]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.logs-container :as logs]
   [webapp.components.timer :as timer]
   [webapp.connections.native-client-access.constants :as constants]))

(defn disconnect-session
  "Handle disconnect with confirmation"
  []
  (let [dialog-text "Are you sure you want to disconnect this native client session?"
        open-dialog #(rf/dispatch [:dialog->open {:text dialog-text
                                                  :type :danger
                                                  :action-button? true
                                                  :on-success (fn []
                                                                (rf/dispatch [:native-client-access->clear-session])
                                                                (rf/dispatch [:draggable-card->close])
                                                                (rf/dispatch [:modal->close]))
                                                  :text-action-button "Disconnect"}])]
    (open-dialog)))

(defn- get-hostname []
  (let [hostname (.-hostname js/location)]
    (if (= hostname "localhost")
      "0.0.0.0"
      hostname)))

(defn not-available-dialog
  "Dialog shown when native client access method is not available"
  [{:keys [error-message]}]

  [:section
   [:header {:class "mb-4"}
    [:> Heading {:size "6" :as "h2"}
     "Connection method not available"]]

   [:main {:class "space-y-4"}
    [:p {:class "text-sm text-gray-600 mb-4"}
     (or error-message
         "This connection method is not available at this moment. Please reach out to your organization admin to enable this method.")]]

   [:footer {:class "flex justify-end mt-6"}
    [:> Button
     {:variant "solid"
      :on-click #(rf/dispatch [:modal->close])}
     "Close"]]])

(defn- configure-session-view
  "Step 1: Configure session duration"
  [connection-name selected-duration requesting?]
  [:> Flex {:direction "column" :justify "between" :gap "8" :class "h-full"}
   [:> Box {:class "space-y-8"}
    [:header {:class "mb-6"}
     [:> Heading {:size "6" :as "h2" :class "text-[--gray-12] mb-2"}
      "Configure session"]
     [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
      "Specify how long you need access to this resource role."]]

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
        :options constants/access-duration-options}]]

     [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
      "Your access will automatically expire after this period"]]]

   [:footer {:class "sticky bottom-0 z-30 bg-white py-10 flex justify-end items-center"}
    [:> Button
     {:variant "solid"
      :size "3"
      :loading @requesting?
      :disabled @requesting?
      :on-click #(rf/dispatch [:native-client-access->request-access
                               connection-name
                               @selected-duration])}
     "Confirm and Connect"]]])

(defn- postgres-credentials-fields
  "PostgreSQL specific credentials fields"
  [native-client-access-data]
  [:> Box {:class "space-y-4"}
   ;; Database Name
   (when (:database_name native-client-access-data)
     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Database Name"]
      [logs/new-container
       {:status :success
        :id "database-name"
        :logs (:database_name native-client-access-data)}]])

   ;; Host
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Host"]
    [logs/new-container
     {:status :success
      :id "hostname"
      :logs (get-hostname)}]]

   ;; Username
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Username"]
    [logs/new-container
     {:status :success
      :id "username"
      :logs (:username native-client-access-data)}]]

   ;; Password
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Password"]
    [logs/new-container
     {:status :success
      :id "password"
      :logs (:password native-client-access-data)}]]

   ;; Port
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Port"]
    [logs/new-container
     {:status :success
      :id "port"
      :logs (:port native-client-access-data)}]]])

(defn- rdp-credentials-fields
  "RDP specific credentials fields"
  [native-client-access-data]
  [:> Box {:class "space-y-4"}

   [:> Callout.Root {:size "1" :color "blue" :class "w-full"}
    [:> Callout.Icon
     [:> Info {:size 16}]]
    [:> Callout.Text
     "Works only with FreeRDP client"]]

   ;; Host
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Host"]
    [logs/new-container
     {:status :success
      :id "hostname"
      :logs (get-hostname)}]]

   ;; Username
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Username"]
    [logs/new-container
     {:status :success
      :id "username"
      :logs (:username native-client-access-data)}]]

   ;; Password
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Password"]
    [logs/new-container
     {:status :success
      :id "password"
      :logs (:password native-client-access-data)}]]

   ;; Port
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Port"]
    [logs/new-container
     {:status :success
      :id "port"
      :logs (:port native-client-access-data)}]]])

(defn- ssh-credentials-fields
  "SSH specific credentials fields"
  [native-client-access-data]
  [:> Box {:class "space-y-4"}

   ;; Host
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Host"]
    [logs/new-container
     {:status :success
      :id "hostname"
      :logs (get-hostname)}]]

   ;; Username
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Username"]
    [logs/new-container
     {:status :success
      :id "username"
      :logs (:username native-client-access-data)}]]

   ;; Password
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Password"]
    [logs/new-container
     {:status :success
      :id "password"
      :logs (:password native-client-access-data)}]]

   ;; Port
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Port"]
    [logs/new-container
     {:status :success
      :id "port"
      :logs (:port native-client-access-data)}]]])

(defn- connect-credentials-tab
  "Credentials tab content - adapts based on connection type"
  [{:keys [connection_type connection_credentials]}]
  [:> Box {:class "space-y-4"}
   (case connection_type
     "postgres" [postgres-credentials-fields connection_credentials]
     "rdp" [rdp-credentials-fields connection_credentials]
     "ssh" [ssh-credentials-fields connection_credentials]
     [postgres-credentials-fields connection_credentials])])

(defn- connect-uri-tab
  "Connection URI tab content - PostgreSQL only"
  [{:keys [connection_credentials]}]
  [:> Box {:class "space-y-4"}
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Connection String"]
    [logs/new-container
     {:status :success
      :id "connection-string"
      :logs (:connection_string connection_credentials)}]]

   [:> Text {:as "p" :size "2" :class "text-[--gray-11] mt-3"}
    "Works with DBeaver, DataGrip and most PostgreSQL clients"]])

(defn- connect-command-tab
  "Command tab content"
  [native-client-access-data]
  [:> Box {:class "space-y-4"}
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Connection Command"]
    [logs/new-container
     {:status :success
      :id "command"
      :logs (:command native-client-access-data)}]]])

(defn- connection-established-view
  "Step 2: Connection established - show credentials"
  [native-client-access-data minimize-fn disconnect-fn]
  (let [active-tab (r/atom "credentials") 
        has-command? (some? (get (:connection_credentials native-client-access-data) :command))]

    (fn []
      [:> Flex {:direction "column" :class "h-full"}
       ;; Scrollable content area
       [:> Box {:class "flex-1 space-y-6 pb-10"}
        [:header {:class "space-y-3"}
         [:> Heading {:size "6" :as "h2" :class "text-[--gray-12]"}
          (str "Connect to " (:connection_name native-client-access-data))]

         [:> Flex {:align "center" :gap "2"}
          [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
           "Connection established, time left: "]
          [timer/inline-timer
           {:expire-at (:expire_at native-client-access-data)
            :text-component (fn [timer-text]
                              [:> Text {:size "3" :weight "bold" :class "text-[--gray-11]"}
                               timer-text])
            :on-complete (fn []
                           (rf/dispatch [:native-client-access->clear-session])
                           (rf/dispatch [:modal->close])
                           (rf/dispatch [:show-snackbar {:level :info
                                                         :text "Native client access session has expired."}]))}]]]

        (cond
          (= (:connection_type native-client-access-data) "postgres")
          [:> Tabs.Root {:value @active-tab
                         :onValueChange #(reset! active-tab %)}
           [:> Tabs.List {:aria-label "Connection methods"}
            [:> Tabs.Trigger {:value "credentials"} "Credentials"]
            [:> Tabs.Trigger {:value "connection-uri"} "Connection URI"]]

           [:> Tabs.Content {:value "credentials" :class "mt-4"}
            [connect-credentials-tab native-client-access-data]]

           [:> Tabs.Content {:value "connection-uri" :class "mt-4"}
            [connect-uri-tab native-client-access-data]]]

          (= (:connection_type native-client-access-data) "ssh")
          [:> Tabs.Root {:value @active-tab
                         :onValueChange #(reset! active-tab %)}
           [:> Tabs.List {:aria-label "Connection methods"}
            [:> Tabs.Trigger {:value "credentials"} "Credentials"]
            (when has-command?
              [:> Tabs.Trigger {:value "command"} "Command"])]

           [:> Tabs.Content {:value "credentials" :class "mt-4"}
            [connect-credentials-tab native-client-access-data]]

           (when has-command?
             [:> Tabs.Content {:value "command" :class "mt-4"}
              [connect-command-tab (:connection_credentials native-client-access-data)]])]

          :else
          [connect-credentials-tab native-client-access-data])]

       ;; Sticky footer
       [:footer {:class "sticky bottom-0 z-30 bg-white py-4 flex justify-between items-center"}
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
    "Your native client access session has expired or is invalid."]
   [:> Button
    {:variant "solid"
     :size "3"
     :on-click #(rf/dispatch [:modal->close])}
    "Close"]])

(defn minimize-modal-content [native-client-access-data]
  [:> Box {:class "min-w-32"}
   [:> Box {:class "space-y-2"}
    [:> Box
     [:> Text {:size "2" :class "text-[--gray-12]"}
      "Connected to: "]
     [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
      (:connection_name native-client-access-data)]]
    [:> Box
     [:> Text {:size "2" :class "text-[--gray-12]"}
      "Type: "]
     [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
      (case (:connection_type native-client-access-data)
        "postgres" "PostgreSQL"
        "rdp" "Remote Desktop"
        "ssh" "SSH"
        "Unknown")]]
    [:> Box
     [:> Text {:size "2" :class "text-[--gray-12]"}
      "Time left: "]
     [timer/inline-timer
      {:expire-at (:expire_at native-client-access-data)
       :text-component (fn [timer-text]
                         [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
                          timer-text])
       :on-complete (fn []
                      (rf/dispatch [:native-client-access->clear-session])
                      (rf/dispatch [:draggable-card->close])
                      (rf/dispatch [:show-snackbar {:level :info
                                                    :text "Native client access session has expired."}]))}]]]

   [:> Box {:class "mt-4"}
    [:> Button
     {:variant "solid"
      :size "1"
      :color "red"
      :on-click disconnect-session}
     "Disconnect"]]])

(defn minimize-modal
  "Minimize modal to draggable card"
  []
  (let [native-client-access-data @(rf/subscribe [:native-client-access->current-session])]
    (rf/dispatch [:modal->close])
    (when native-client-access-data
      (rf/dispatch [:draggable-card->open
                    {:component [minimize-modal-content native-client-access-data]
                     :on-click-expand (fn []
                                        (rf/dispatch [:draggable-card->close])
                                        (rf/dispatch [:native-client-access->reopen-connect-modal]))}]))))

(defn main
  "Main native client access component - manages the complete flow"
  [connection-name-or-map]
  (let [selected-duration (r/atom 30)
        requesting? (rf/subscribe [:native-client-access->requesting?])
        native-client-access-data (rf/subscribe [:native-client-access->current-session])
        session-valid? (rf/subscribe [:native-client-access->session-valid?])
        connection-name (if (string? connection-name-or-map)
                          connection-name-or-map
                          (:name connection-name-or-map))
        session-matches-connection? (and @native-client-access-data
                                         (= (:id @native-client-access-data) connection-name))]

    [:> Box {:class "flex max-h-[696px] overflow-hidden -m-radix-5"}
     [:> Flex {:direction "column" :justify "between" :gap "6" :class "w-full px-10 pt-10 overflow-y-auto"}
      ;; Main content based on current state
      (cond
        ;; Step 2: Connected - show connection details (only if session matches requested connection)
        (and @session-valid? @native-client-access-data session-matches-connection?)
        [connection-established-view @native-client-access-data minimize-modal disconnect-session]

        ;; Step 1: Configure session duration (no session or session for different connection)
        (or (not @native-client-access-data) (not session-matches-connection?))
        [configure-session-view connection-name selected-duration requesting?]

        ;; Fallback: Session expired
        :else
        [session-expired-view])]

     [:> Box {:class "min-w-[525px] bg-blue-50 max-h-[696px]"}
      [:img {:src "/images/illustrations/cli-promotion.png"
             :alt  "cli illustration"
             :class "w-full h-full object-cover"}]]]))
