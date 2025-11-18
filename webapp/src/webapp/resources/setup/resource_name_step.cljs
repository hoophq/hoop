(ns webapp.resources.setup.resource-name-step
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex Grid Heading Text]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.constants :as conn-constants]
   [webapp.connections.views.resource-catalog.helpers :as helpers]))

(defn main []
  (let [resource-name (rf/subscribe [:resource-setup/resource-name])
        resource-type (rf/subscribe [:resource-setup/resource-type])
        resource-subtype (rf/subscribe [:resource-setup/resource-subtype])
        current-connection-metadata (rf/subscribe [:resource-setup/current-connection-metadata])]
    (fn []
      (let [badge (helpers/get-connection-badge @current-connection-metadata)
            icon-url (conn-constants/get-connection-icon {:type @resource-type
                                                          :subtype @resource-subtype}
                                                         "default")]
        [:form {:id "resource-name-form"
                :on-submit (fn [e]
                             (.preventDefault e)
                             ;; Validate name before proceeding
                             (rf/dispatch [:resource-setup->validate-resource-name
                                           @resource-name
                                           [:resource-setup->next-step :agent-selector]]))}
         [:> Box {:class "p-8 space-y-16"}
          ;; Header
          [:> Box
           [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-gray-12"}
            "Setup your Resource"]
           [:> Text {:as "p" :size "3" :class "text-gray-12"}
            "Complete the following information to setup your Resource."]]

          [:> Grid {:columns "7" :gap "7"}
           [:> Box {:grid-column "span 3 / span 3"}
            [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
             "Resource type"]
            [:> Text {:size "2" :class "text-[--gray-11]"}
             "This name is used to identify your Agent in your environment."]]

           [:> Flex {:grid-column "span 4 / span 4" :direction "column" :justify "between"
                     :class "h-[110px] p-radix-4 rounded-lg border border-gray-3 bg-white"}

            [:> Flex {:gap "3" :align "center" :justify "between"}
             (when icon-url
               [:img {:src icon-url
                      :class "w-6 h-6"
                      :alt (or @resource-subtype "resource")}])

             [:> Flex {:gap "1"}
              (when (seq badge)
                (for [badge badge]
                  ^{:key (:text badge)}
                  [:> Badge {:color (:color badge)
                             :size "1"
                             :variant "solid"}
                   (:text badge)]))]]

            [:> Box
             [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
              (if @resource-subtype
                (case @resource-subtype
                  "linux-vm" "Linux VM"
                  "ssh" "SSH"
                  "tcp" "TCP"
                  "httpproxy" "HTTP Proxy"
                  (:name @current-connection-metadata))
                "Loading...")]]]]

          ;; Resource Name Input
          [:> Grid {:columns "7" :gap "7"}
           [:> Box {:grid-column "span 3 / span 3"}
            [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
             "Name"]
            [:> Text {:size "2" :class "text-[--gray-11]"}
             "Used to identify this Resource in your environment."]]

           [:> Box {:grid-column "span 4 / span 4"}
            [:> Box {:class "space-y-1"}
             [forms/input {:label "Resource name"
                           :placeholder (str "e.g. my-" @resource-subtype)
                           :value @resource-name
                           :required true
                           :on-change #(let [value (-> % .-target .-value)
                                             ;; Replace spaces with hyphens automatically
                                             sanitized-value (cs/replace value #"\s+" "-")]
                                         (rf/dispatch [:resource-setup->set-resource-name sanitized-value]))}]]]]]]))))

