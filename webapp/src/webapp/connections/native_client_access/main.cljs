(ns webapp.connections.native-client-access.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Tabs Text]]
   ["lucide-react" :refer [Info]]
   [clojure.edn :as edn]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.logs-container :as logs]
   [webapp.components.timer :as timer]
   [webapp.connections.native-client-access.constants :as constants]))

(defn disconnect-session
  "Handle disconnect with confirmation"
  [connection-name]
  (let [dialog-text (str "Are you sure you want to disconnect the native client session for \"" connection-name "\"?")
        open-dialog #(rf/dispatch [:dialog->open {:text dialog-text
                                                  :type :danger
                                                  :action-button? true
                                                  :on-success (fn []
                                                                (rf/dispatch [:native-client-access->clear-session connection-name])
                                                                (rf/dispatch [:modal->close]))
                                                  :text-action-button "Disconnect"}])]
    (open-dialog)))

(defn- get-hostname []
  (let [hostname (.-hostname js/location)]
    (if (= hostname "localhost")
      "0.0.0.0"
      hostname)))

(defn- get-ssl-mode
  "Determine SSL mode based on current page protocol"
  []
  (if (= (.-protocol js/location) "https:")
    "require"
    "disable"))

(defn- build-postgres-connection-string
  [{:keys [port username password database_name]}]
  (let [db-name (or database_name "postgres")
        ssl-mode (get-ssl-mode)
        hostname (get-hostname)]
    (str "postgres://" username ":" password "@" hostname ":" port "/" db-name "?sslmode=" ssl-mode)))

(defn- build-ssh-command
  [{:keys [port username]}]
  (let [hostname (get-hostname)]
    (str "ssh " username "@" hostname " -p " port)))

(defn- build-aws-ssm-command
  [{:keys [aws_access_key_id aws_secret_access_key endpoint_url]}]
  (let [hostname (get-hostname)
        origin (.-origin js/location)
        endpoint-url (if (= hostname "localhost")
                       (or endpoint_url (str origin "/ssm"))
                       (str origin "/ssm"))]
    (str "AWS_ACCESS_KEY_ID=\"" aws_access_key_id "\" "
         "AWS_SECRET_ACCESS_KEY=\"" aws_secret_access_key "\" "
         "aws ssm start-session --target {TARGET_INSTANCE} "
         "--endpoint-url \"" endpoint-url "\"")))

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
  [connection-credentials]
  [:> Box {:class "space-y-4"}
   ;; Database Name
   (when (:database_name connection-credentials)
     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Database Name"]
      [logs/new-container
       {:status :success
        :id "database-name"
        :logs (:database_name connection-credentials)}]])

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
      :logs (:username connection-credentials)}]]

   ;; Password
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Password"]
    [logs/new-container
     {:status :success
      :id "password"
      :logs (:password connection-credentials)}]]

   ;; Port
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Port"]
    [logs/new-container
     {:status :success
      :id "port"
      :logs (:port connection-credentials)}]]])

(defn- rdp-credentials-fields
  "RDP specific credentials fields"
  [connection-credentials]
  [:> Box {:class "space-y-4"}

   [:> Callout.Root {:size "1" :color "blue" :class "w-full"}
    [:> Callout.Icon
     [:> Info {:size 16}]]
    [:> Callout.Text
     "Works only with Web Client"]]])

(defn- http-proxy-credentials-fields
  "Http proxy specific credentials fields"
  [{:keys [command port proxy_token]}]

  (let [{:keys [curl browser]} (some-> command js/JSON.parse (js->clj :keywordize-keys true))]
    [:> Box {:class "space-y-4"}

   ;; Host
     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Host"]
      [logs/new-container
       {:status :success
        :id "hostname"
        :logs (get-hostname)}]]
     ;;Authorization token
     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Authorization Header"]
      [logs/new-container
       {:status :success
        :id "authtoken"
        :logs proxy_token}]]
     ;; Commands
     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Command cURL"]
      [logs/new-container
       {:status :success
        :id "command"
        :logs curl}]]

     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Command Browser"]
      [logs/new-container
       {:status :success
        :id "command-browser"
        :logs browser}]]

   ;; Port
     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Port"]
      [logs/new-container
       {:status :success
        :id "port"
        :logs port}]]]))

(defn- ssh-credentials-fields
  "SSH specific credentials fields"
  [connection-credentials]
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
      :logs (:username connection-credentials)}]]

   ;; Password
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Password"]
    [logs/new-container
     {:status :success
      :id "password"
      :logs (:password connection-credentials)}]]

   ;; Port
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Port"]
    [logs/new-container
     {:status :success
      :id "port"
      :logs (:port connection-credentials)}]]])

(defn- connect-credentials-tab
  "Credentials tab content - adapts based on connection type"
  [{:keys [connection_type connection_credentials]}]
  [:> Box {:class "space-y-4"}
   (case connection_type
     "postgres" [postgres-credentials-fields connection_credentials]
     "rdp" [rdp-credentials-fields connection_credentials]
     "ssh" [ssh-credentials-fields connection_credentials]
     "httpproxy" [http-proxy-credentials-fields connection_credentials]
     [postgres-credentials-fields connection_credentials])])

(defn- connect-uri-tab
  "Connection URI tab content - PostgreSQL only"
  [{:keys [connection_credentials]}]
  (let [connection-string (build-postgres-connection-string connection_credentials)]
    [:> Box {:class "space-y-4"}
     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Connection String"]
      [logs/new-container
       {:status :success
        :id "connection-string"
        :logs connection-string}]]

     [:> Text {:as "p" :size "2" :class "text-[--gray-11] mt-3"}
      "Works with DBeaver, DataGrip and most PostgreSQL clients"]]))

(defn- connect-command-tab
  "Command tab content"
  [command-text]
  [:> Box {:class "space-y-4"}
   [:> Box {:class "space-y-2"}
    [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
     "Command"]
    [logs/new-container
     {:status :success
      :id "command"
      :logs command-text}]]])

(defn- aws-ssm-command-view
  [native-client-access-data]
  (let [connection-credentials (:connection_credentials native-client-access-data)
        command (build-aws-ssm-command connection-credentials)]
    [:> Box {:class "space-y-8"}
     [:> Box {:class "space-y-2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Command"]
      [logs/new-container
       {:status :success
        :id "aws-ssm-command"
        :logs command}]]

     [:> Callout.Root {:size "1" :class "w-full"}
      [:> Callout.Icon
       [:> Info {:size 16}]]
      [:> Callout.Text
       "These credentials are valid for 30 minutes starting now"]]]))

(defn- connection-established-view
  "Step 2: Connection established - show credentials"
  [connection-name native-client-access-data minimize-fn disconnect-fn]
  (let [active-tab (r/atom "credentials")
        connection-type (or (:connection_type native-client-access-data)
                            (:subtype native-client-access-data))
        has-command? (contains? #{"ssh" "rdp"} connection-type)]

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
                           (rf/dispatch [:native-client-access->clear-session connection-name])
                           (rf/dispatch [:modal->close])
                           (rf/dispatch [:show-snackbar {:level :info
                                                         :text "Native client access session has expired."}]))}]]]

        (cond
          (= connection-type "postgres")
          [:> Tabs.Root {:value @active-tab
                         :onValueChange #(reset! active-tab %)}
           [:> Tabs.List {:aria-label "Connection methods"}
            [:> Tabs.Trigger {:value "credentials"} "Credentials"]
            [:> Tabs.Trigger {:value "connection-uri"} "Connection URI"]]

           [:> Tabs.Content {:value "credentials" :class "mt-4"}
            [connect-credentials-tab native-client-access-data]]

           [:> Tabs.Content {:value "connection-uri" :class "mt-4"}
            [connect-uri-tab native-client-access-data]]]

          (= connection-type "ssh")
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
              [connect-command-tab (build-ssh-command (:connection_credentials native-client-access-data))]])]

          (= connection-type "aws-ssm")
          [aws-ssm-command-view native-client-access-data]

          (= connection-type "httpproxy")
          [:> Tabs.Root {:value @active-tab
                         :onValueChange #(reset! active-tab %)}
           [:> Tabs.List {:aria-label "Connection methods"}
            [:> Tabs.Trigger {:value "credentials"} "Credentials"]]

           [:> Tabs.Content {:value "credentials" :class "mt-4"}
            [connect-credentials-tab native-client-access-data]]]

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

        (when (= connection-type "rdp")
          [:> Button
           {:variant "solid"
            :size "3"
            :on-click #(rf/dispatch [:native-client-access->open-rdp-web-client
                                     (get-in native-client-access-data [:connection_credentials :username])])}
           "Open Web Client"])

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

(defn minimize-modal-content [connection-name native-client-access-data]
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
      (case (or (:connection_type native-client-access-data)
                (:subtype native-client-access-data))
        "postgres" "PostgreSQL"
        "rdp" "Remote Desktop"
        "ssh" "SSH"
        "aws-ssm" "AWS SSM"
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
                      (rf/dispatch [:native-client-access->clear-session connection-name])
                      (rf/dispatch [:show-snackbar {:level :info
                                                    :text "Native client access session has expired."}]))}]]]

   [:> Box {:class "mt-4"}
    [:> Button
     {:variant "solid"
      :size "1"
      :color "red"
      :on-click #(disconnect-session connection-name)}
     "Disconnect"]]])

(defn minimize-modal
  "Minimize modal to draggable card"
  [connection-name]
  (let [native-client-access-data @(rf/subscribe [:native-client-access->current-session connection-name])]
    (rf/dispatch [:modal->close])
    (when native-client-access-data
      (rf/dispatch [:draggable-cards->open
                    connection-name
                    {:component [minimize-modal-content connection-name native-client-access-data]
                     :on-click-expand (fn []
                                        (rf/dispatch [:draggable-cards->close connection-name])
                                        (rf/dispatch [:native-client-access->reopen-connect-modal connection-name]))}]))))

(defn main
  "Main native client access component - manages the complete flow"
  [connection-name-or-map]
  (let [connection-name (if (string? connection-name-or-map)
                          connection-name-or-map
                          (:name connection-name-or-map))
        selected-duration (r/atom 30)
        requesting? (rf/subscribe [:native-client-access->requesting? connection-name])
        native-client-access-data (rf/subscribe [:native-client-access->current-session connection-name])
        session-valid? (rf/subscribe [:native-client-access->session-valid? connection-name])]

    [:> Box {:class "flex max-h-[696px] overflow-hidden -m-radix-5"}
     [:> Flex {:direction "column" :justify "between" :gap "6" :class "w-full px-10 pt-10 overflow-y-auto"}
      ;; Main content based on current state
      (cond
        ;; Step 2: Connected - show connection details
        (and @session-valid? @native-client-access-data)
        [connection-established-view connection-name @native-client-access-data
         #(minimize-modal connection-name)
         #(disconnect-session connection-name)]

        ;; Step 1: Configure session duration (no session)
        (not @native-client-access-data)
        [configure-session-view connection-name selected-duration requesting?]

        ;; Fallback: Session expired
        :else
        [session-expired-view])]

     [:> Box {:class "min-w-[525px] bg-blue-50 max-h-[696px]"}
      [:img {:src "/images/illustrations/cli-promotion.png"
             :alt  "cli illustration"
             :class "w-full h-full object-cover"}]]]))
