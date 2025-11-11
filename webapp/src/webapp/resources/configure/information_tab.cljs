(ns webapp.resources.configure.information-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text TextField]]
   [clojure.string :as cs]
   [reagent.core :as r]
   [re-frame.core :as rf]
   [webapp.connections.constants :as conn-constants]))

(defn main [resource]
  (let [icon-url (conn-constants/get-connection-icon {:type (:type resource)
                                                      :subtype (:subtype resource)}
                                                     "default")
        new-name (r/atom (:name resource))
        updating? (rf/subscribe [:resources->updating?])]
    (fn [resource]
      [:> Box {:class "space-y-16"}
       ;; Resource type
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
                   :alt (or (:subtype resource) "resource")}])]

         [:> Box
          [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
           (cs/capitalize (:type resource))]]]]

       ;; Resource Name
       [:> Grid {:columns "7" :gap "7"}
        [:> Box {:grid-column "span 3 / span 3"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
          "Resource ID"]
         [:> Text {:size "2" :class "text-[--gray-11]"}
          "Used to identify this Resource in your environment."]]

        [:> Box {:grid-column "span 4 / span 4"}
         [:> Flex {:gap "2" :align "end"}
          [:> Box {:class "flex-1"}
           [:> Box {:class "space-y-1"}
            [:> Text {:as "label" :size "2" :weight "medium" :class "text-[--gray-12]"}
             "Name"]
            [:> TextField.Root
             {:value @new-name
              :onChange #(reset! new-name (-> % .-target .-value))
              :disabled @updating?
              :placeholder "Enter resource name"}]]]
          [:> Button {:size "2"
                      :variant "solid"
                      :disabled (or @updating?
                                    (cs/blank? @new-name)
                                    (= @new-name (:name resource)))
                      :on-click (fn []
                                  (when (and (not (cs/blank? @new-name))
                                             (not= @new-name (:name resource)))
                                    (rf/dispatch [:resources->update-resource-name (:name resource) @new-name])))}
           "Save"]]]]])))
