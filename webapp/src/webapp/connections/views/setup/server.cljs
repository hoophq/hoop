;; server.cljs
(ns webapp.connections.views.setup.server
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Button Card Flex Grid Heading
                               RadioGroup Text]]
   ["lucide-react" :refer [Blocks SquareTerminal]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
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

(defn main []
  (let [connection-subtype @(rf/subscribe [:connection-setup/connection-subtype])
        app-type @(rf/subscribe [:connection-setup/app-type])
        os-type @(rf/subscribe [:connection-setup/os-type])]
    [page-wrapper/main
     {:children
      [:> Box {:class "max-w-2xl mx-auto p-6"}
       [headers/setup-header]
       [:> Box {:class "space-y-8"}
        [:> Box {:class "space-y-4"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
          "Connection type"]
         (for [[subtype {:keys [icon title subtitle]}] connections-subtypes-cards]
           (let [is-selected (= subtype connection-subtype)]
             ^{:key subtype}
             [:> Card {:size "1"
                       :variant "surface"
                       :class (str "w-full cursor-pointer " (when is-selected "before:bg-primary-12"))
                       :on-click #(rf/dispatch [:connection-setup/select-subtype subtype])}
              [:> Flex {:align "center" :gap "3"}
               [:> Avatar {:size "4"
                           :class (when is-selected "dark")
                           :variant "soft"
                           :color "gray"
                           :fallback icon}]
               [:> Flex {:direction "column" :class (str "" (when is-selected "text-gray-4"))}
                [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
                [:> Text {:size "2" :color "gray-11"} subtitle]]]]))]

           ;; Conteúdo específico baseado na seleção
        (case connection-subtype
          "ssh" [:<>
                 [environment-variables-section]
                 [configuration-files-section]]

          "console" [:<>
                     [:> Box
                      [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
                       "Application type"]
                      [:> Text {:as "p" :size "3" :class "text-[--gray-11]" :mb "5"}
                       "Select stack type for your application connection."]

                      [:> RadioGroup.Root
                       {:value app-type
                        :on-value-change #(rf/dispatch [:connection-setup/select-app-type %])}
                       [:> Flex {:direction "column" :gap "4"}
                        (for [{:keys [id title]} application-types]
                          ^{:key id}
                          [:> RadioGroup.Item {:value id}
                           title])]]]

                     (when app-type
                       [:> Box
                        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]" :mb "5"}
                         "Operating system"]

                        [:> RadioGroup.Root
                         {:value os-type
                          :on-value-change #(rf/dispatch [:connection-setup/select-os-type %])}
                         [:> Flex {:direction "column" :gap "4"}
                          (for [{:keys [id title]} operation-systems]
                            ^{:key id}
                            [:> RadioGroup.Item {:value id}
                             title])]]])]
          [:<>])]]

      :footer-props {:next-text "Next: Configuration"
                    ;;  (if (= current-step :additional-config)
                    ;;    "Confirm"
                    ;;    "Next: Configuration")
                     :next-disabled? (or (not connection-subtype)
                                         (and (= connection-subtype "console")
                                              (not app-type)))
                     :on-next #(rf/dispatch [:connection-setup/next-step])
                    ;;  (if (= current-step :additional-config)
                    ;;    #(rf/dispatch [:connection-setup/submit])
                    ;;    #(rf/dispatch [:connection-setup/next-step]))
                     }}]))


