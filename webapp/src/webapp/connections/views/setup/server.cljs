;; server.cljs
(ns webapp.connections.views.setup.server
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Card Flex Heading RadioGroup Text]]
   ["lucide-react" :refer [Blocks SquareTerminal]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.configuration-inputs :as configuration-inputs]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.installation :as installation]
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
      "Add an additional command that will run on your connection."
      [:br]
      "Environment variables loaded above can also be used here."]
     [forms/textarea
      {:label "Command"
       :placeholder "$ bash"
       :value @(rf/subscribe [:connection-setup/command])
       :on-change #(rf/dispatch [:connection-setup/set-command
                                 (-> % .-target .-value)])}]]

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

      (when (= connection-subtype "ssh")
        [credentials-step])

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
                             {:selected-type connection-subtype
                              :form-type form-type
                              :submit-fn (if (= connection-subtype "console")
                                           #(rf/dispatch [:connection-setup/next-step :installation])
                                           #(rf/dispatch [:connection-setup/submit]))}]
         :installation [installation/main]
         [resource-step])]

      :footer-props
      {:form-type form-type
       :next-text (case current-step
                    :credentials (if (= connection-subtype "ssh")
                                   "Next: Configuration"
                                   "Next")
                    :additional-config (if (= connection-subtype "console")
                                         "Next: Installation"
                                         "Confirm")
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
                                                 (if (= current-step :credentials)
                                                   "credentials-form"
                                                   "additional-config-form"))]
                       (.reportValidity form))))
       :on-next (case current-step
                  :additional-config (if (= connection-subtype "console")
                                       #(rf/dispatch [:connection-setup/next-step :installation])
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
                    (let [form (.getElementById js/document "credentials-form")]
                      (when form
                        (if (and (.reportValidity form)
                                 agent-id)
                          (let [event (js/Event. "submit" #js {:bubbles true :cancelable true})]
                            (.dispatchEvent form event))
                          (js/console.warn "Invalid form!"))))))}}]))


