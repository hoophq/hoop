(ns webapp.connection-details.views.panel
  (:require
   [re-frame.core :as rf]
   [webapp.audit.views.sessions-list :as sessions-list]
   [webapp.audit.views.session-item :as session-item]
   [webapp.connection-details.views.how-to-connect :as how-to-connect]
   [webapp.connections.views.connection-form-modal :as connection-form-modal]
   [webapp.connections.views.connection-connect :as connection-connect]
   [webapp.components.button :as button]
   [webapp.components.icon :as icon]
   [webapp.components.headings :as h]))

(defn runbook-item [runbook connection]
  [:div
   {:class "flex items-center gap-x-small"}
   [icon/hero-icon {:size 5
                    :icon "document-black"}]
   [:a {:href (str "/runbooks?selected="
                   (:name runbook)
                   "&connection="
                   (:name connection))
        :class "text-xs text-gray-700 transition hover:text-blue-500"}
    (:name runbook)]])

(defn- sessions-list-view [sessions status user]
  [:div {:class "relative overflow-hidden border rounded-lg"}
   (when (and (= status :loading) (empty? (:data sessions)))
     [:div {:class "py-large"}
      [sessions-list/loading-list-view]])
   (when (and (empty? (:data sessions)) (not= status :loading))
     [sessions-list/empty-list-view])
   (doall
     ;; inforce having five due to this state is shared between sessions page
    (for [session (take 8 (:data sessions))]
      ^{:key (:id session)}
      [:div {:class (when (= status :loading) "opacity-50 pointer-events-none")}
       [session-item/session-item session user]]))])

(def card-classes "rounded-lg bg-white py-regular px-large")
(defn panel [_]
  (let [user (rf/subscribe [:users->current-user])
        my-plugins (rf/subscribe [:plugins->my-plugins])
        connection (rf/subscribe [:connections->connection-details])
        sessions (rf/subscribe [:audit])
        runbooks-by-connection (rf/subscribe [:runbooks-plugin->runbooks-by-connection])]

    (fn [current-connection]
      (let [admin? (-> @user :data :admin?)
            free-license? (-> @user :data :free-license?)
            active-plugins (filter (fn [plugin]
                                     (some #(= (:name current-connection) (:name %)) (:connections plugin))) @my-plugins)
            editor-plugin? (some #(= "editor" (:name %)) active-plugins)
            connection-type (-> @connection :data :type)]
        [:div {:class "grid grid-cols-1 gap-regular"}
         ;; header
         [:header {:class "pl-regular"}
          [:div {:class "flex flex-col lg:flex-row gap-regular lg:gap-0"}
           [:div {:class "flex-grow"}
            [h/h1 (:name current-connection)]
            [:div {:class "text-xs text-gray-500"}
             connection-type]]

           [:div {:class "flex flex-col-reverse items-end lg:flex-row gap-regular"}
            (when editor-plugin?
              [button/secondary {:text [:div {:class "flex items-center gap-small"}
                                        [:span "Go to Web Client"]
                                        [icon/hero-icon {:icon "code-black"}]]
                                 :outlined true
                                 :on-click #(rf/dispatch [:navigate :editor-plugin])}])
            (when (and (not= "command-line" connection-type)
                       (not (true? (:loading @connection))))
              [button/secondary {:on-click (fn []
                                             (rf/dispatch [:connections->connection-connect (:name current-connection)])
                                             (rf/dispatch [:draggable-card->open-modal
                                                           [connection-connect/main]
                                                           :default
                                                           connection-connect/handle-close-modal]))
                                 :outlined true
                                 :text [:span {:class "flex items-center gap-small"}
                                        [:span "Connect"]
                                        [icon/regular {:size 4
                                                       :icon-name "cable-black"}]]}])
            (when (and admin? (not (= (:managed_by current-connection) "hoopagent")))
              [button/black {:on-click (fn []
                                         (rf/dispatch [:connections->get-connection {:connection-name (:name current-connection)}])
                                         (rf/dispatch [:open-modal [connection-form-modal/main :update]
                                                       :large]))
                             :text [:span {:class "flex items-center gap-small"}
                                    [icon/hero-icon {:size 6
                                                     :icon "settings-white"}]]}])]]]
         ;; end header

         [:div {:class (str "grid grid-cols-1 gap-regular "
                            (when (not free-license?)
                              "lg:grid-cols-2"))}
          ;; runbooks
          (when (not free-license?)
            [:div {:class (str card-classes)}
             [:header {:class "mb-regular"}
              [h/h2 "Runbooks"]
              [:span {:class "text-xs text-gray-500"}
               "List of Runbooks available for this connection"]]
             (when (= :error (:status @runbooks-by-connection))
               [:div {:class "py-large flex flex-col text-xs text-center"}
                [:span
                 {:class "font-bold text-gray-800"}
                 "No Runbooks available for this connection"]
                [:a {:href "https://hoop.dev/docs/features/runbooks"
                     :target "_blank"
                     :class "text-blue-500 hover:underline"}
                 "See how to set-up Runbooks"]])
             (when (@runbooks-by-connection :data)
               [:section {:class "grid grid-cols-1 gap-small"}
                (doall
                 (for [runbook (-> @runbooks-by-connection :data :items)]
                   ^{:key (:name runbook)}
                   [runbook-item runbook current-connection]))])])
          ;; end runbooks

          ;; how to access
          [:div {:class (str card-classes)}
           [:header {:class "mb-regular"}
            [h/h2 "How to access"]
            [:span {:class "text-xs text-gray-500"}
             "Follow these instructions access this connection"]]
           [:section
            [how-to-connect/main @connection]]]]
         ;; end how to access


         ;; sessions
         [:div {:class (str card-classes)}
          [:header {:class "flex items-center mb-regular"}
           [:div {:class "flex-grow"}
            [h/h2 "Sessions"]
            [:span {:class "text-xs text-gray-500"}
             "Latest sessions for this connection"]]
           (when editor-plugin?
             [button/black {:text [:div {:class "flex items-center gap-small"}
                                   [:span "Go to Web Client"]
                                   [icon/hero-icon {:icon "code-white"}]]
                            :variant :small
                            :on-click #(rf/dispatch [:navigate :editor-plugin])}])]
          [sessions-list-view (:sessions @sessions) (:status @sessions) @user]
          [:footer {:class "p-small text-right"}
           [:a {:href (str "/connections/" (:id current-connection) "/sessions?connection=" (:name current-connection))
                :class "text-blue-500 text-xs hover:underline"}
            "See all sessions for this connection"]]]]))))
         ;; end sessions

(defn main [_]
  (fn [current-connection]
    (rf/dispatch [:connections->get-connection-details (:name current-connection)])
    (rf/dispatch [:runbooks-plugin->get-runbooks-by-connection (:name current-connection)])
    (rf/dispatch [:audit->get-sessions 5 {"limit" 8
                                          "connection" (:name current-connection)}])
    (rf/dispatch [:agents->get-agents])
    [panel current-connection]))
