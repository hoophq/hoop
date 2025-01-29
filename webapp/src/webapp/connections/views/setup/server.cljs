;; server.cljs
(ns webapp.connections.views.setup.server
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Card Flex Grid Heading
                               RadioGroup Text]]
   ["lucide-react" :refer [Blocks SquareTerminal]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]
   [webapp.connections.views.setup.state :refer [application-types
                                                 operation-systems]]))

(def connections-subtypes-cards
  {"ssh" {:icon (r/as-element [:> SquareTerminal {:size 18}])
          :title "Linux VM or Container"
          :subtitle "Secure shell protocol (SSH) for remote access."}
   "console" {:icon (r/as-element [:> Blocks {:size 18}])
              :title "Console"
              :subtitle "For Ruby on Rails, Python, Node JS and more."}})

(defn environment-variables-section []
  (let [env-vars (rf/subscribe [:connection-setup/environment-variables])
        current-key (r/atom "")
        current-value (r/atom "")]
    (fn []
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} "Environment variables"]
     [:> Text {:size "2" :color "gray"}
      "Add variable values to use in your connection."]

     ;; Lista de variáveis existentes
     (for [{:keys [key value]} @env-vars]
       ^{:key key}
       [:> Flex {:gap "2" :my "2"}
        [:> Text {:size "2"} key ": " value]])

     ;; Campos para nova variável
     [:> Grid {:columns "2" :gap "2"}
      [forms/input
       {:label "Key"
        :value @current-key
        :on-change #(reset! current-key (-> % .-target .-value))}]
      [forms/input
       {:label "Value"
        :value @current-value
        :type "password"
        :on-change #(reset! current-value (-> % .-target .-value))}]]

     [:> Button
      {:size "2"
       :variant "soft"
       :on-click #(when (and @current-key @current-value)
                    (rf/dispatch [:connection-setup/add-environment-variable
                                  @current-key @current-value])
                    (reset! current-key "")
                    (reset! current-value ""))}
      "Add"]])))

(defn configuration-files-section []
  (let [current-file (r/atom {:name "" :content ""})]
    (fn []
    [:> Box {:class "space-y-4"}
     [:> Heading {:size "3"} "Configuration files"]

     [forms/input
      {:label "Name"
       :placeholder "e.g. kube_config"
       :value (:name @current-file)
       :on-change #(swap! current-file assoc :name (-> % .-target .-value))}]

     [forms/textarea
      {:label "Content"
       :placeholder "Paste your file content here"
       :value (:content @current-file)
       :on-change #(swap! current-file assoc :content (-> % .-target .-value))}]

     [:> Button
      {:size "2"
       :variant "soft"
       :on-click #(when (and (:name @current-file) (:content @current-file))
                    (rf/dispatch [:connection-setup/add-configuration-file @current-file])
                    (reset! current-file {:name "" :content ""}))}
      "Add"]])))

(defn credentials-step []
  [:> Box {:class "space-y-8"}
   ;; Environment Variables Section
   [environment-variables-section]

   ;; Configuration Files Section
   [configuration-files-section]

   ;; Additional Command Section
   [:> Box {:class "space-y-4"}
    [:> Heading {:size "3"} "Additional command"]
    [:> Text {:size "2" :color "gray"}
     "Add an additional command that will run on your connection."]
    #_[forms/input
     {:label "Command"
      :placeholder "$ your command"
      :value @(rf/subscribe [:connection-setup/command])
      :on-change #(rf/dispatch [:connection-setup/update-command
                                (-> % .-target .-value)])}]]

   ;; Agent Section
   [agent-selector/main]])

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


(defn installation-step []
  [:> Box {:class "space-y-7"}
   [headers/console-all-done-header]

   [:> Box {:class "space-y-5"}
    [:> Heading {:size "4" :weight "bold"} "Install hoop.dev CLI"]
    [:> Box {:class "bg-gray-900 text-white p-4 rounded-md font-mono text-sm"}
     "brew tap hoophq/brew https://github.com/hoophq/brew\nbrew install hoop"]

    [:> Heading {:size "4" :weight "bold" :mt "5"} "Setup token"]
    [:> Box {:class "bg-gray-900 text-white p-4 rounded-md font-mono text-sm"}
     "export HOOP_KEY=$API_KEY"]

    [:> Heading {:size "4" :weight "bold" :mt "5"} "Run your connection"]
    [:> Box {:class "bg-gray-900 text-white p-4 rounded-md font-mono text-sm"}
     "hoop run --name your-connection --command python3"]]])

(defn get-next-step [current-step connection-subtype]
  (js/console.log "Current Step:" current-step "Subtype:" connection-subtype)
  (case current-step
    :resource (do
                (js/console.log "Inside resource case")
                (case connection-subtype
                  "ssh" (do
                          (js/console.log "SSH selected - going to credentials")
                          :credentials)
                  "console" :app-type
                  :resource))
    :app-type :os-type
    :os-type :additional-config
    :credentials :additional-config
    :additional-config (if (= connection-subtype "console")
                         :installation
                         :submit)
    :installation :submit
    :resource))

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
                    :on-click #(rf/dispatch [:connection-setup/select-subtype subtype])}
           [:> Flex {:align "center" :gap "3"}
            [:> Avatar {:size "4"
                        :class (when is-selected "dark")
                        :variant "soft"
                        :color "gray"
                        :fallback icon}]
            [:> Flex {:direction "column"}
             [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
             [:> Text {:size "2" :color "gray-11"} subtitle]]]]))]

     (when (= connection-subtype "ssh")
       [credentials-step])

     ;; Application Type Selection - mostrar somente se Console estiver selecionado
     (when (= connection-subtype "console")
       [application-type-step])

     ;; Operating System Selection - mostrar somente se o app-type estiver selecionado
     (when (and (= connection-subtype "console") app-type)
       [operating-system-step])]))


(defn main []
  (let [connection-subtype @(rf/subscribe [:connection-setup/connection-subtype])
        current-step @(rf/subscribe [:connection-setup/current-step])
        app-type @(rf/subscribe [:connection-setup/app-type])
        os-type @(rf/subscribe [:connection-setup/os-type])]

    [page-wrapper/main
     {:children
      [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
       [headers/setup-header]

       (case current-step
         :resource [resource-step]
         :additional-config [additional-configuration/main
                             {:selected-type connection-subtype}]
         :installation [installation-step]
         [resource-step])]

      :footer-props
      {:next-text (case current-step
                    :resource "Next: Configuration"
                    :additional-config (if (= connection-subtype "console")
                                         "Next: Installation"
                                         "Confirm")
                    :installation "Done"
                    "Next")
       ;; Só habilitar o Next quando todos os campos necessários estiverem preenchidos
       :next-disabled? (case current-step
                         :resource (or
                                    (not connection-subtype)
                                    (case connection-subtype
                                      "console" (or (not app-type)
                                                    (not os-type))
                                      "ssh" false  ;; Pode ser ajustado se precisar validar campos do Linux VM
                                      true))
                         false)
       :on-next #(if (= current-step :installation)
                   (rf/dispatch [:connection-setup/submit])
                   (rf/dispatch [:connection-setup/next-step
                                 (get-next-step current-step connection-subtype)]))}}]))


