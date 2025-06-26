;; server.cljs
(ns webapp.connections.views.setup.server
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Card Flex Grid Heading RadioGroup Text]]
   ["lucide-react" :refer [Blocks SquareTerminal]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.connections.constants :refer [connection-configs-required]]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.installation :as installation]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]
   [webapp.connections.views.setup.state :refer [application-types
                                                 operation-systems]]))

(def connections-subtypes-cards
  {"custom" {:icon (r/as-element [:> SquareTerminal {:size 18}])
             :title "Linux VM or Container"
             :subtitle "Secure shell protocol (SSH) for remote access."}
   "ssh" {:icon (r/as-element [:> SquareTerminal {:size 18}])
          :title "Secure Shell Protocol (SSH)"
          :subtitle "Access and manage with terminal commands."}
   "console" {:icon (r/as-element [:> Blocks {:size 18}])
              :title "Console"
              :subtitle "For Ruby on Rails, Python, Node JS and more."}})

(defn credentials-step []
  [:form
   {:id "credentials-form"
    :on-submit (fn [e]
                 (.preventDefault e)
                 (rf/dispatch [:connection-setup/next-step :additional-config]))}
   [:> Box {:class "space-y-8 max-w-[600px]"}
    ;; Environment Variables Section
    [configuration-inputs/environment-variables-section]

    ;; Configuration Files Section
    [configuration-inputs/configuration-files-section]

    ;; Additional Command Section
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} "Additional command"]
     [:> Text {:size "2" :color "gray"}
      "Each argument should be entered separately."
      [:br]
      "Press Enter after each argument to add it to the list."]
     [:> Box
      [multi-select/text-input
       {:value @(rf/subscribe [:connection-setup/command-args])
        :input-value @(rf/subscribe [:connection-setup/command-current-arg])
        :on-change #(rf/dispatch [:connection-setup/set-command-args %])
        :on-input-change #(rf/dispatch [:connection-setup/set-command-current-arg %])
        :label "Command Arguments"
        :id "command-args"
        :name "command-args"}]
      [:> Text {:size "2" :color "gray" :mt "2"}
       "Example: 'python', '-m', 'http.server', '8000'"]]]

    ;; Agent Section
    [agent-selector/main]]])

(defn application-type-step []
  [:> Box {:class "space-y-5"}
   [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
    "Application type"]
   [:> Text {:as "p" :size "3" :class "text-[--gray-11]" :mb "5"}
    "Select stack type for your application connection."]

   [:> RadioGroup.Root
    {:value @(rf/subscribe [:connection-setup/app-type])
     :on-value-change #(rf/dispatch [:connection-setup/select-app-type %])}
    [:> Flex {:direction "column" :gap "4"}
     (for [{:keys [id title]} application-types]
       ^{:key id}
       [:> RadioGroup.Item {:value id} title])]]])

(defn operating-system-step []
  [:> Box {:class "space-y-5"}
   [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
    "Operating system"]

   [:> RadioGroup.Root
    {:value @(rf/subscribe [:connection-setup/os-type])
     :on-value-change #(rf/dispatch [:connection-setup/select-os-type %])}
    [:> Flex {:direction "column" :gap "4"}
     (for [{:keys [id title]} operation-systems]
       ^{:key id}
       [:> RadioGroup.Item {:value id} title])]]])

(defn render-ssh-field [{:keys [key label value required hidden placeholder type]}]
  (let [base-props {:label label
                    :placeholder (or placeholder (str "e.g. " key))
                    :value value
                    :required required
                    :type (if (= key "pass") "password" "text")
                    :hidden hidden
                    :on-change #(rf/dispatch [:connection-setup/update-ssh-credentials
                                              key
                                              (-> % .-target .-value)])}]
    (if (= type "textarea")
      [forms/textarea base-props]
      [forms/input base-props])))

;; Registrar um evento para controlar o método de autenticação
(rf/reg-event-db
 :connection-setup/set-ssh-auth-method
 (fn [db [_ method]]
   (assoc-in db [:connection-setup :ssh-auth-method] method)))

;; Registrar um subscription para acessar o método de autenticação
(rf/reg-sub
 :connection-setup/ssh-auth-method
 (fn [db]
   (get-in db [:connection-setup :ssh-auth-method] "password"))) ;; "password" ou "key"

(defn ssh-credentials []
  (let [configs (get connection-configs-required :ssh)
        credentials @(rf/subscribe [:connection-setup/ssh-credentials])
        auth-method @(rf/subscribe [:connection-setup/ssh-auth-method])
        filtered-fields (filter (fn [field]
                                  (case auth-method
                                    "password" (not= (:key field) "authorized_server_keys")
                                    "key" (not= (:key field) "pass")
                                    true))
                                configs)]
    [:form
     {:id "ssh-credentials-form"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:connection-setup/next-step :additional-config]))}
     [:> Box {:class "space-y-8 max-w-[600px]"}
      [:> Box {:class "space-y-4"}
       [:> Box
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "SSH Configuration"]
        [:> Text {:as "p" :size "3" :class "text-[--gray-11]" :mb "5"}
         "Provide SSH information to setup your connection."]]

       [:> Box {:class "space-y-4 mb-6"}
        [:> Heading {:as "h4" :size "3" :weight "medium"}
         "Authentication Method"]
        [:> RadioGroup.Root
         {:value auth-method
          :on-value-change #(rf/dispatch [:connection-setup/set-ssh-auth-method %])}
         [:> Flex {:direction "column" :gap "2"}
          [:> RadioGroup.Item {:value "password"} "Username & Password"]
          [:> RadioGroup.Item {:value "key"} "Private Key Authentication"]]]]

       [:> Grid {:columns "1" :gap "4"}
        (for [field filtered-fields]
          ^{:key (:key field)}
          [render-ssh-field (assoc field
                                   :value (get credentials (:key field) (:value field)))])

        [agent-selector/main]]]]]))

(defn resource-step []
  (let [connection-subtype @(rf/subscribe [:connection-setup/connection-subtype])
        app-type @(rf/subscribe [:connection-setup/app-type])
        os-type @(rf/subscribe [:connection-setup/os-type])]
    [:> Box {:class "space-y-7"}
     ;; Connection Type Selection
     [:> Box {:class "space-y-4"}
      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
       "Connection type"]
      (for [[subtype {:keys [icon title subtitle]}] connections-subtypes-cards]
        (let [is-selected (= subtype connection-subtype)]
          ^{:key subtype}
          [:> Card {:size "1"
                    :variant "surface"
                    :class (str "w-full cursor-pointer "
                                (when is-selected "before:bg-primary-12"))
                    :on-click #(rf/dispatch [:connection-setup/select-connection "server" subtype])}
           [:> Flex {:align "center" :gap "3" :class (str (when is-selected "text-[--gray-1]"))}
            [:> Avatar {:size "4"
                        :class (when is-selected "dark")
                        :variant "soft"
                        :color "gray"
                        :fallback icon}]
            [:> Flex {:direction "column"}
             [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
             [:> Text {:size "2" :color "gray-11"} subtitle]]]]))]

     (when (= connection-subtype "custom")
       [credentials-step])

     (when (= connection-subtype "ssh")
       [ssh-credentials])

     (when (= connection-subtype "console")
       [application-type-step])

     (when (and app-type (not os-type))
       [operating-system-step])]))


(defn main [form-type]
  (let [connection-subtype @(rf/subscribe [:connection-setup/connection-subtype])
        current-step @(rf/subscribe [:connection-setup/current-step])
        app-type @(rf/subscribe [:connection-setup/app-type])
        os-type @(rf/subscribe [:connection-setup/os-type])
        agent-id @(rf/subscribe [:connection-setup/agent-id])]

    [page-wrapper/main
     {:children
      [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
       (if (= current-step :installation)
         [headers/console-all-done-header]
         [headers/setup-header form-type])

       (case current-step
         :credentials [resource-step]
         :additional-config [additional-configuration/main
                             {:show-database-schema? (= connection-subtype "cloudwatch")
                              :selected-type connection-subtype
                              :form-type form-type
                              :submit-fn (cond
                                           (= connection-subtype "console")
                                           #(rf/dispatch [:connection-setup/next-step :installation])

                                           (= connection-subtype "ssh")
                                           #(rf/dispatch [:connection-setup/submit])

                                           :else
                                           #(rf/dispatch [:connection-setup/submit]))}]
         :installation [installation/main]
         [resource-step])]

      :footer-props
      {:form-type form-type
       :next-text (case current-step
                    :credentials (cond
                                   (= connection-subtype "console") "Next"
                                   (= connection-subtype "ssh") "Next: Configuration"
                                   :else "Next: Configuration")
                    :additional-config (cond
                                         (= connection-subtype "console") "Next: Installation"
                                         (= connection-subtype "ssh") "Confirm"
                                         :else "Confirm")
                    :installation "Done"
                    "Next")
       :next-disabled? (case current-step
                         :credentials (or (not connection-subtype)
                                          (and (= connection-subtype "console")
                                               (or (not app-type)
                                                   (not os-type))))
                         nil)
       :on-click (fn []
                   (when-not (= current-step :installation)
                     (let [form (.getElementById js/document
                                                 (cond
                                                   (= current-step :credentials)
                                                   (if (= connection-subtype "ssh")
                                                     "ssh-credentials-form"
                                                     "credentials-form")

                                                   :else
                                                   "additional-config-form"))]
                       (.reportValidity form))))
       :on-next (case current-step
                  :additional-config (cond
                                       (= connection-subtype "console")
                                       #(rf/dispatch [:connection-setup/next-step :installation])

                                       :else
                                       (fn []
                                         (let [form (.getElementById js/document "additional-config-form")]
                                           (when form
                                             (if (and (.reportValidity form)
                                                      agent-id)
                                               (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                                                 (.dispatchEvent form event))
                                               (js/console.warn "Invalid form!"))))))
                  :installation (fn []
                                  (rf/dispatch [:navigate :connections])
                                  (rf/dispatch [:connection-setup/initialize-state nil]))
                  (fn []
                    (let [form-id (if (= connection-subtype "ssh")
                                    "ssh-credentials-form"
                                    "credentials-form")
                          form (.getElementById js/document form-id)]
                      (when form
                        (if (and (.reportValidity form)
                                 agent-id)
                          (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                            (.dispatchEvent form event))
                          (js/console.warn "Invalid form!"))))))}}]))


