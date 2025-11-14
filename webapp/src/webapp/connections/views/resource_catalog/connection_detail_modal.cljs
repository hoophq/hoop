(ns webapp.connections.views.resource-catalog.connection-detail-modal
  (:require
   ["@radix-ui/themes" :refer [Avatar Badge Box Button Card Dialog Flex
                               Heading Link ScrollArea Tabs Text]]
   ["lucide-react" :refer [BookMarked Monitor SquareTerminal]]
   [reagent.core :as r]
   [webapp.components.text-with-markdown-link :as text-with-markdown-link]
   [webapp.connections.views.resource-catalog.helpers :as helpers]))


(defn navigate-to-setup
  "Navigate to setup flow with pre-selected connection type"
  [connection]
  (if-let [action-result (helpers/execute-special-action connection)]

    action-result

    (let [setup-config (helpers/get-setup-config connection)]
      (if setup-config
        (helpers/dispatch-setup-navigation
         setup-config
         (helpers/is-onboarding-context?))
        (js/console.warn "No setup mapping found for connection:" (:id connection))))))

(defn modal-overview-tab [overview setupGuide]
  [:> Box {:class "space-y-6"}
   (when (:description overview)
     [:> Box
      [text-with-markdown-link/main
       (:description overview)
       {:size "3" :class "text-gray-12"}
       {:size "3" :target "_blank" :class "text-blue-12"}]])

   (when-let [access-methods (get-in setupGuide [:accessMethods])]
     [:div
      [:> Text {:size "3" :weight "bold" :class "block mb-4 text-gray-900"}
       "Connection Methods"]
      [:div {:class "grid grid-cols-2 gap-4"}
       (when (:webapp access-methods)
         [:> Card {:size "1"}
          [:> Flex {:direction "column" :gap "3"}
           [:> Flex {:align "center" :gap "2"}
            [:> Avatar {:size "4"
                        :variant "soft"
                        :color "gray"
                        :fallback (r/as-element [:> Monitor {:size 18}])}]
            [:> Box
             [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-gray-12"} "Web App"]
             [:> Text {:as "p" :size "2" :class "text-gray-11"}
              "Access resources and execute commands directly from Web UI."]]]]])

       (when (:runbooks access-methods)
         [:> Card {:size "1"}
          [:> Flex {:direction "column" :gap "3"}
           [:> Flex {:align "center" :gap "2"}
            [:> Avatar {:size "4"
                        :variant "soft"
                        :color "gray"
                        :fallback (r/as-element [:> BookMarked {:size 18}])}]
            [:> Box
             [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-gray-12"} "Runbooks"]
             [:> Text {:as "p" :size "2" :class "text-gray-11"}
              "Execute securely git-based predefined scripts in your resources."]]]]])

       (when (:cli access-methods)
         [:> Card {:size "1"}
          [:> Flex {:direction "column" :gap "3"}
           [:> Flex {:align "center" :gap "2"}
            [:> Avatar {:size "4"
                        :variant "soft"
                        :color "gray"
                        :fallback (r/as-element [:> SquareTerminal {:size 18}])}]
            [:> Box
             [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-gray-12"} "Hoop CLI"]
             [:> Text {:as "p" :size "2" :class "text-gray-11"}
              "Access resources and execute commands natively in your favorite apps."]]]]])]])])

(defn modal-setup-tab [connection]
  [:div {:class "space-y-6"}
   (when-let [credentials (get-in connection [:resourceConfiguration :credentials])]
     [:div
      [:> Text {:size "3" :weight "bold" :class "block mb-4 text-gray-900"}
       "Configuration"]
      [:div {:class "space-y-3"}
       (for [credential-info credentials]
         ^{:key (:name credential-info)}
         [:> Card {:size "1"}
          [:> Flex {:direction "column" :gap "2"}
           [:> Flex {:align "center" :justify "between"}
            [:> Flex {:align "center" :gap "2"}
             [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-gray-12"}
              (:name credential-info)]
             (when (:required credential-info)
               [:> Badge {:size "1"} "Required"])]
            [:> Badge {:variant "soft" :color "gray" :size "1"}
             (case (:type credential-info)
               "env-var" "Environment Variable"
               "filesystem" "File Content"
               "textarea" "Text Content"
               (:type credential-info))]]
           [text-with-markdown-link/main
            (:description credential-info)
            {:size "2" :class "text-gray-11"}
            {:size "2" :target "_blank" :class "text-blue-11"}]]])]])])

(defn main [connection open? on-close]
  (when connection
    (let [{:keys [name description overview setupGuide]} connection
          badge (helpers/get-connection-badge connection)]

      [:> Dialog.Root {:open open?
                       :onOpenChange #(when-not % (on-close))}
       [:> Dialog.Content {:size "4"
                           :max-width "1000px"
                           :class "max-h-[85vh] overflow-hidden"}
        [:> Flex {:align "center" :justify "between" :gap "3"}
         [:> Box {:class "w-[60%]"}
          [:> Dialog.Title
           [:> Flex {:align "center" :items "center" :gap "2"}
            [:> Text {:size "8" :weight "bold" :class "text-gray-12"}
             name]

            (when (seq badge)
              (for [badge badge]
                ^{:key (:text badge)}
                [:> Badge {:color (:color badge)
                           :size "1"
                           :variant "solid"}
                 (:text badge)]))]]

          [:> Dialog.Description {:class "mb-6"}
           [:> Text {:color "gray" :size "3"} description]]]

         [:> Flex {:gap "3" :class "mb-6"}
          [:> Link {:href (str "https://hoop.dev/docs/"
                               (get-in connection [:documentationConfig :path]))
                    :target "_blank"}
           [:> Button {:variant "soft"
                       :size "3"}
            "View Docs"]]
          [:> Button {:variant "solid" :size "3"
                      :on-click #(navigate-to-setup connection)}
           "Continue Setup"]]]

        [:> Tabs.Root {:default-value "overview" :class "w-full"}
         [:> Tabs.List {:class "border-b border-gray-200 mb-6"}
          [:> Tabs.Trigger {:value "overview" :class "pb-3 text-sm font-medium"}
           "Overview"]
          (when (get-in connection [:resourceConfiguration :credentials])
            [:> Tabs.Trigger {:value "setup-guide" :class "pb-3 text-sm font-medium"}
             "Setup Guide"])]

         [:> Tabs.Content {:value "overview" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-overview-tab overview setupGuide]]]

         [:> Tabs.Content {:value "setup-guide" :class "outline-none"}
          [:> ScrollArea {:class "max-h-[400px] overflow-auto pr-4"}
           [modal-setup-tab connection]]]]]])))
